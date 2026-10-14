package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

type pairingClient interface {
	confirmationSender
	ConnectContext(context.Context) error
	Disconnect()
	AddEventHandler(whatsmeow.EventHandler) uint32
	GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error)
	PairPhone(context.Context, string, bool, whatsmeow.PairClientType, string) (string, error)
	Logout(context.Context) error
	IsOnWhatsApp(context.Context, []string) ([]types.IsOnWhatsAppResponse, error)
	GetUserInfo(context.Context, []types.JID) (map[types.JID]types.UserInfo, error)
	GetProfilePictureInfo(context.Context, types.JID, *whatsmeow.GetProfilePictureParams) (*types.ProfilePictureInfo, error)
}

type connectionState struct {
	State     string    `json:"state"`
	Method    string    `json:"method,omitempty"`
	Code      string    `json:"code,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

type accountConnection struct {
	mu sync.Mutex
	connectionState
	phone        string
	done         chan struct{}
	proof        []byte
	logout       chan chan error
	numberChecks chan numberCheck
	profiles     chan profileRequest
}

func (c *accountConnection) state() connectionState {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.connectionState
	if !s.ExpiresAt.IsZero() && !time.Now().Before(s.ExpiresAt) {
		s.Code = ""
	}
	return s
}

func (c *accountConnection) update(state, code string, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.State, c.Code, c.ExpiresAt = state, code, expires
}

type whatsAppService struct {
	mu                sync.Mutex
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	db                *sql.DB
	store             *sqlstore.Container
	accounts          map[string]*accountConnection
	newClient         func(*store.Device) pairingClient
	developmentSignup bool
}

func openWhatsAppService(ctx context.Context, dsn string) (*whatsAppService, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid WHATSAPP_DATABASE_URL")
	}
	config.RuntimeParams["search_path"] = "whatsapp"
	config.ConnectTimeout = 10 * time.Second
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(40)
	db.SetMaxIdleConns(5)
	container := sqlstore.NewWithDB(db, "pgx", nil)
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = db.PingContext(readyCtx); err == nil {
		_, err = db.ExecContext(readyCtx, "CREATE SCHEMA IF NOT EXISTS whatsapp")
	}
	if err == nil {
		err = container.Upgrade(readyCtx)
	}
	if err != nil {
		_ = db.Close()
		return nil, errors.New("WhatsApp storage setup failed; check credentials, schema permissions and server availability")
	}
	serviceCtx, stop := context.WithCancel(ctx)
	s := &whatsAppService{ctx: serviceCtx, cancel: stop, db: db, store: container, accounts: make(map[string]*accountConnection)}
	s.newClient = func(device *store.Device) pairingClient {
		client := whatsmeow.NewClient(device, nil)
		client.InitialAutoReconnect = true
		client.DisableLoginAutoReconnect = true
		return client
	}
	return s, nil
}

func (s *whatsAppService) close() {
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
	_ = s.store.Close()
}

func (s *whatsAppService) start(user actor, id, method, phone string, proof ...[]byte) (*accountConnection, error) {
	user.BusinessID, id = strings.ToLower(user.BusinessID), strings.ToLower(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return nil, errors.New("service_stopped")
	}
	key := user.BusinessID + "/" + id
	var loginProof []byte
	if len(proof) > 0 {
		loginProof = proof[0]
	}
	if c := s.accounts[key]; c != nil {
		select {
		case <-c.done:
		default:
			if method == "" && c.state().State == "connected" {
				return c, nil
			}
			if !bytes.Equal(c.proof, loginProof) {
				return nil, errors.New("connection_in_progress")
			}
			if c.Method != method || c.phone != phone {
				return nil, errors.New("connection_in_progress")
			}
			return c, nil
		}
	}
	// shortcut: at most 32 active accounts and 64 status entries; raise these with the worker pool capacity.
	active := 0
	for key, c := range s.accounts {
		select {
		case <-c.done:
			if len(s.accounts) >= 64 {
				delete(s.accounts, key)
			}
		default:
			active++
		}
	}
	if active >= 32 {
		return nil, errors.New("connection_capacity")
	}
	c := &accountConnection{connectionState: connectionState{State: "connecting", Method: method}, phone: phone, done: make(chan struct{}), proof: loginProof, logout: make(chan chan error), numberChecks: make(chan numberCheck), profiles: make(chan profileRequest)}
	s.accounts[key] = c
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(c.done)
		for {
			state := s.run(user, id, c)
			if state != "busy" || c.Method != "" {
				c.update(state, "", time.Time{})
				return
			}
			// A replacement worker waits for the previous deployment to release its locks.
			c.update("waiting_for_worker", "", time.Time{})
			retry := time.NewTimer(time.Second)
			select {
			case <-s.ctx.Done():
				retry.Stop()
				c.update("disconnected", "", time.Time{})
				return
			case result := <-c.logout:
				retry.Stop()
				result <- errors.New("remote_unlink_unavailable")
				c.update("needs_relink", "", time.Time{})
				return
			case <-retry.C:
			}
		}
	}()
	return c, nil
}

func (s *whatsAppService) state(businessID, id string) (connectionState, bool) {
	s.mu.Lock()
	c := s.accounts[strings.ToLower(businessID+"/"+id)]
	s.mu.Unlock()
	if c == nil {
		return connectionState{}, false
	}
	return c.state(), true
}

const activeWhatsAppAccount = `SELECT wa.device_jid FROM ipo_pos.whatsapp_accounts wa
    JOIN ipo_pos.business_memberships m ON m.business_id=wa.business_id AND m.user_id=wa.linked_by_user_id
    JOIN ipo_pos.users u ON u.id=m.user_id JOIN ipo_pos.businesses b ON b.id=m.business_id
    WHERE wa.business_id=$1 AND wa.id=$2 AND wa.linked_by_user_id=$3
    AND m.disabled_at IS NULL AND u.disabled_at IS NULL AND b.archived_at IS NULL
    AND (wa.device_jid IS NULL OR wa.unlinked_at IS NULL)`

const pendingSignupAccount = `SELECT NULL::text FROM ipo_pos.login_attempts a
    WHERE a.signup_business_id=$1 AND a.signup_account_id=$2 AND a.signup_user_id=$3 AND a.token_hash=$4
    AND a.state='pending' AND a.expires_at>now() AND a.consumed_at IS NULL
    AND NOT EXISTS (SELECT 1 FROM ipo_pos.users WHERE phone_number=a.phone_number)`

func (s *whatsAppService) run(user actor, id string, c *accountConnection) string {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return "storage_unavailable"
	}
	// Returning a session-locked connection to a pool would retain its lock.
	defer func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }); _ = conn.Close() }()
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", "ipo-whatsapp/"+user.BusinessID+"/"+id).Scan(&locked); err != nil {
		return "storage_unavailable"
	}
	if !locked {
		return "busy"
	}
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", "ipo-whatsapp-user/"+user.UserID).Scan(&locked); err != nil {
		return "storage_unavailable"
	}
	if !locked {
		return "busy"
	}
	var otherBinding bool
	if err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ipo_pos.whatsapp_accounts WHERE linked_by_user_id=$1 AND (business_id,id)<>($2,$3) AND device_jid IS NOT NULL)`, user.UserID, user.BusinessID, id).Scan(&otherBinding); err != nil {
		return "storage_unavailable"
	}
	if otherBinding {
		return "already_paired"
	}
	var saved sql.NullString
	accountQuery := activeWhatsAppAccount
	accountArgs := []any{user.BusinessID, id, user.UserID}
	if s.developmentSignup && len(c.proof) > 0 {
		accountQuery += " UNION ALL " + pendingSignupAccount
		accountArgs = append(accountArgs, c.proof)
	}
	if err = conn.QueryRowContext(ctx, accountQuery, accountArgs...).Scan(&saved); err != nil {
		return "account_unavailable"
	}
	if err = recoverConfirmations(ctx, conn, user.BusinessID, id); err != nil {
		return "storage_unavailable"
	}
	device := s.store.NewDevice()
	if saved.Valid {
		if c.Method != "" {
			return "already_paired"
		}
		jid, parseErr := types.ParseJID(saved.String)
		if parseErr != nil {
			return "invalid_binding"
		}
		device, err = s.store.GetDevice(ctx, jid)
		if err != nil {
			return "storage_unavailable"
		}
		if device == nil {
			if s.clearBinding(ctx, user, id, saved.String) != nil {
				return "storage_unavailable"
			}
			return "needs_relink"
		}
	} else if c.Method == "" {
		return "needs_relink"
	}
	client := s.newClient(device)
	defer func() { cancel(); client.Disconnect() }()
	lifecycle := make(chan any, 16)
	client.AddEventHandler(func(evt any) {
		switch evt.(type) {
		case *events.PairSuccess, *events.Connected, *events.Disconnected, *events.ManualLoginReconnect, *events.Picture, events.PermanentDisconnect:
			select {
			case lifecycle <- evt:
			case <-ctx.Done():
			}
		}
	})
	var qr <-chan whatsmeow.QRChannelItem
	if !saved.Valid {
		qr, err = client.GetQRChannel(ctx)
		if err != nil {
			return "pairing_failed"
		}
	}
	if client.ConnectContext(ctx) != nil {
		return "connection_failed"
	}
	checks := time.NewTicker(15 * time.Second)
	defer checks.Stop()
	var expiry <-chan time.Time
	var timer *time.Timer
	if !saved.Valid {
		timer = time.NewTimer(3 * time.Minute)
		defer timer.Stop()
		expiry = timer.C
	}
	phoneRequested := false
	deliveries := time.NewTicker(2 * time.Second)
	defer deliveries.Stop()
	for {
		select {
		case request := <-c.profiles:
			profileCtx, stop := context.WithCancel(request.ctx)
			stopOnShutdown := context.AfterFunc(ctx, stop)
			var current sql.NullString
			err = conn.QueryRowContext(profileCtx, activeWhatsAppAccount, user.BusinessID, id, user.UserID).Scan(&current)
			result := profileResult{err: err}
			if errors.Is(err, sql.ErrNoRows) || (err == nil && (current != saved || !saved.Valid || c.state().State != "connected")) {
				result.err = errProfileUnavailable
			} else if err == nil {
				result.profile, result.err = lookupContactProfile(profileCtx, client, device.Contacts, request.phone)
				if result.err == nil {
					result.profile.Revision, result.err = saveProfileObservation(profileCtx, s.db, user, id, saved.String,
						request.phone, result.profile.PictureState, result.profile.pictureID, time.Time{})
				}
			}
			stopOnShutdown()
			stop()
			request.result <- result
		case request := <-c.numberChecks:
			checkCtx, stop := context.WithCancel(request.ctx)
			stopOnShutdown := context.AfterFunc(ctx, stop)
			var current sql.NullString
			err = conn.QueryRowContext(checkCtx, activeWhatsAppAccount, user.BusinessID, id, user.UserID).Scan(&current)
			result := numberCheckResult{err: err}
			if errors.Is(err, sql.ErrNoRows) || (err == nil && (current != saved || !saved.Valid || c.state().State != "connected")) {
				result.err = errNumberCheckUnavailable
			} else if err == nil {
				result.registered, result.err = checkRegisteredNumber(checkCtx, client, request.phone)
				if result.err == nil {
					result.err = conn.QueryRowContext(checkCtx, `INSERT INTO ipo_pos.whatsapp_number_checks
						(business_id,phone_number,registered,whatsapp_account_id) VALUES($1,$2,$3,$4)
						ON CONFLICT (business_id,phone_number) DO UPDATE
						SET registered=EXCLUDED.registered,checked_at=clock_timestamp(),whatsapp_account_id=EXCLUDED.whatsapp_account_id
						RETURNING checked_at`, user.BusinessID, request.phone, result.registered, id).Scan(&result.checkedAt)
					if result.err != nil && checkCtx.Err() == nil {
						result.err = errNumberCheckStorage
					} else if checkCtx.Err() != nil {
						result.err = checkCtx.Err()
					}
				}
			}
			stopOnShutdown()
			stop()
			request.result <- result
		case result := <-c.logout:
			logoutCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			logoutErr := client.Logout(logoutCtx)
			stop()
			result <- logoutErr
			return "needs_relink"
		case <-ctx.Done():
			return "disconnected"
		case <-expiry:
			return "expired"
		case <-deliveries.C:
			if c.state().State == "connected" {
				if err = deliverConfirmation(ctx, conn, user, id, saved.String, client); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return "account_unavailable"
					}
					return "storage_unavailable"
				}
			}
		case <-checks.C:
			checkCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			var current sql.NullString
			err = conn.QueryRowContext(checkCtx, accountQuery, accountArgs...).Scan(&current)
			stop()
			if err != nil || current != saved {
				return "account_unavailable"
			}
		case item, ok := <-qr:
			if !ok {
				return "pairing_failed"
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				if c.Method == "phone" {
					if !phoneRequested {
						phoneRequested = true
						phoneCtx, stop := context.WithTimeout(ctx, 15*time.Second)
						code, phoneErr := client.PairPhone(phoneCtx, c.phone, false, whatsmeow.PairClientChrome, "Chrome (Windows)")
						stop()
						if phoneErr != nil {
							return "pairing_failed"
						}
						c.update("awaiting_phone", code, time.Now().Add(2*time.Minute))
					}
				} else {
					c.update("awaiting_qr", item.Code, time.Now().Add(item.Timeout))
				}
			case whatsmeow.QRChannelSuccess.Event:
				qr = nil
				if c.state().State != "connected" {
					c.update("finishing_pairing", "", time.Time{})
				}
			case whatsmeow.QRChannelScannedWithoutMultidevice.Event:
				// The same QR can be scanned after enabling Linked devices.
			default:
				return "pairing_failed"
			}
		case evt := <-lifecycle:
			switch evt.(type) {
			case *events.Picture:
				if saved.Valid && c.state().State == "connected" {
					eventCtx, stop := context.WithTimeout(ctx, 5*time.Second)
					err = s.pictureChanged(eventCtx, device, user, id, saved.String, evt.(*events.Picture))
					stop()
					if err != nil {
						return "storage_unavailable"
					}
				}
			case *events.PairSuccess, *events.Connected:
				jid := device.GetJID()
				if jid.Server != types.DefaultUserServer || jid.Device == 0 || !phonePattern.MatchString("+"+jid.User) || (c.Method == "phone" && "+"+jid.User != c.phone) {
					if !saved.Valid {
						cleanup, stop := context.WithTimeout(ctx, 5*time.Second)
						_ = client.Logout(cleanup)
						stop()
					}
					return "invalid_identity"
				}
				// shortcut: a crash after key persistence needs fresh pairing; add recovery if interrupted pairings become frequent.
				var proof []byte
				if !saved.Valid {
					proof = c.proof
				}
				if s.bind(ctx, user, id, jid.String(), "+"+jid.User, proof) != nil {
					if !saved.Valid {
						cleanup, stop := context.WithTimeout(ctx, 5*time.Second)
						_ = client.Logout(cleanup)
						stop()
					}
					return "binding_failed"
				}
				saved = sql.NullString{String: jid.String(), Valid: true}
				c.update("finishing_pairing", "", time.Time{})
				if _, connected := evt.(*events.Connected); connected {
					expiry = nil
					c.update("connected", "", time.Time{})
				}
			case *events.ManualLoginReconnect:
				client.Disconnect()
				if client.ConnectContext(ctx) != nil {
					return "connection_failed"
				}
			case *events.Disconnected:
				c.update("reconnecting", "", time.Time{})
			case *events.LoggedOut:
				if s.clearBinding(ctx, user, id, saved.String) != nil {
					return "storage_unavailable"
				}
				return "needs_relink"
			case events.PermanentDisconnect:
				return "connection_failed"
			}
		}
	}
}

func (s *whatsAppService) bind(ctx context.Context, user actor, id, jid, phone string, proof []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(proof) > 0 {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ipo-login/"+phone); err != nil {
			return err
		}
		var signup bool
		err = tx.QueryRowContext(ctx, `SELECT signup_account_id IS NOT NULL FROM ipo_pos.login_attempts WHERE token_hash=$1
		AND ((user_id=$2 AND whatsapp_account_id=$3 AND business_id=$6)
            OR ($5 AND signup_user_id=$2 AND signup_account_id=$3 AND signup_business_id=$6))
		AND phone_number=$4 AND state='pending' AND expires_at>now() AND consumed_at IS NULL FOR UPDATE`, proof, user.UserID, id, phone, s.developmentSignup, user.BusinessID).Scan(&signup)
		if err != nil {
			return err
		}
		if signup {
			// shortcut: phone-based names and an isolated dev business; replace with master-admin provisioning before production onboarding.
			if _, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.users(id,name,phone_number) VALUES($1,$2,$2)`, user.UserID, phone); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.businesses(id,name) VALUES($1,$2)`, user.BusinessID, "Development business "+phone); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.business_memberships(business_id,user_id,role) VALUES($1,$2,'admin')`, user.BusinessID, user.UserID); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.whatsapp_accounts(business_id,id,label,linked_by_user_id) VALUES($1,$2,'POS login',$3)`, user.BusinessID, id, user.UserID); err != nil {
				return err
			}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE ipo_pos.whatsapp_accounts wa
        SET device_jid=$4,phone_number=$5,unlinked_at=NULL
        WHERE wa.business_id=$1 AND wa.id=$2 AND wa.linked_by_user_id=$3
        AND (wa.device_jid IS NULL OR wa.device_jid=$4)
        AND EXISTS (SELECT 1 FROM ipo_pos.business_memberships m JOIN ipo_pos.users u ON u.id=m.user_id
            JOIN ipo_pos.businesses b ON b.id=m.business_id
            WHERE m.business_id=wa.business_id AND m.user_id=wa.linked_by_user_id
            AND m.disabled_at IS NULL AND u.disabled_at IS NULL AND b.archived_at IS NULL)`, user.BusinessID, id, user.UserID, jid, phone)
	if err == nil {
		if n, countErr := result.RowsAffected(); countErr != nil || n != 1 {
			return errors.New("account unavailable")
		}
	}
	if err != nil {
		return err
	}
	if len(proof) > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE ipo_pos.login_attempts a SET state='verified',auth_generation=wa.auth_generation,
            user_id=wa.linked_by_user_id,business_id=wa.business_id,whatsapp_account_id=wa.id
		FROM ipo_pos.whatsapp_accounts wa WHERE a.token_hash=$1 AND wa.business_id=$2 AND wa.id=$3`, proof, user.BusinessID, id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *whatsAppService) clearBinding(ctx context.Context, user actor, id, jid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ipo_pos.whatsapp_accounts SET device_jid=NULL,unlinked_at=now()
        WHERE business_id=$1 AND id=$2 AND linked_by_user_id=$3 AND device_jid=$4`, user.BusinessID, id, user.UserID, jid)
	return err
}

func (s *whatsAppService) resume() error {
	rows, err := s.db.QueryContext(s.ctx, `SELECT wa.business_id,wa.id,wa.linked_by_user_id FROM ipo_pos.whatsapp_accounts wa
        JOIN ipo_pos.business_memberships m ON m.business_id=wa.business_id AND m.user_id=wa.linked_by_user_id
        JOIN ipo_pos.users u ON u.id=m.user_id JOIN ipo_pos.businesses b ON b.id=m.business_id
        WHERE wa.device_jid IS NOT NULL AND wa.unlinked_at IS NULL
        AND m.disabled_at IS NULL AND u.disabled_at IS NULL AND b.archived_at IS NULL ORDER BY wa.created_at,wa.id LIMIT 32`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var user actor
		var id string
		if err = rows.Scan(&user.BusinessID, &id, &user.UserID); err != nil {
			return err
		}
		if _, err = s.start(user, id, "", ""); err != nil {
			return err
		}
	}
	return rows.Err()
}
