package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

type appSession struct {
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token"`
	TokenType         string    `json:"token_type"`
	ExpiresAt         time.Time `json:"expires_at"`
	RefreshExpiresAt  time.Time `json:"refresh_expires_at"`
	SessionID         string    `json:"session_id"`
	UserID            string    `json:"user_id"`
	WhatsAppAccountID string    `json:"whatsapp_account_id"`
}

// Lock bindings before sessions/tokens, matching the revocation trigger's lock order.
func lockSessionLink(ctx context.Context, tx *sql.Tx, user, business, account string, generation int64) error {
	var valid bool
	return tx.QueryRowContext(ctx, `SELECT true FROM ipo_pos.whatsapp_accounts wa
        JOIN ipo_pos.users u ON u.id=wa.linked_by_user_id
        JOIN ipo_pos.business_memberships m ON m.business_id=wa.business_id AND m.user_id=u.id
        JOIN ipo_pos.businesses b ON b.id=m.business_id
        WHERE wa.business_id=$1 AND wa.id=$2 AND wa.linked_by_user_id=$3 AND wa.auth_generation=$4
        AND wa.device_jid IS NOT NULL AND wa.unlinked_at IS NULL AND u.disabled_at IS NULL
        AND m.disabled_at IS NULL AND b.archived_at IS NULL FOR SHARE OF wa,u,m,b`, business, account, user, generation).Scan(&valid)
}

func issueLinkedSession(ctx context.Context, tx *sql.Tx, user, business, account string, generation int64, name, existingID string, expectedRefresh []byte) (appSession, error) {
	err := lockSessionLink(ctx, tx, user, business, account, generation)
	if err != nil {
		return appSession{}, err
	}
	if existingID != "" {
		// Binding locks precede session locks, matching the revocation trigger's order.
		var valid bool
		err = tx.QueryRowContext(ctx, `SELECT true FROM ipo_pos.login_sessions WHERE id=$1 AND user_id=$2
		AND refresh_hash=$3 AND refresh_expires_at>now() FOR UPDATE`, existingID, user, expectedRefresh).Scan(&valid)
		if err != nil {
			return appSession{}, err
		}
	}
	token, hash, err := newSecret()
	if err != nil {
		return appSession{}, err
	}
	refresh, refreshHash, err := newSecret()
	if err != nil {
		return appSession{}, err
	}
	now := time.Now().UTC()
	result := appSession{AccessToken: token, RefreshToken: refresh, TokenType: "Bearer", ExpiresAt: now.Add(15 * time.Minute), RefreshExpiresAt: now.Add(30 * 24 * time.Hour), UserID: user, WhatsAppAccountID: account}
	err = tx.QueryRowContext(ctx, `INSERT INTO ipo_pos.login_sessions
        (token_hash,user_id,expires_at,business_id,whatsapp_account_id,auth_generation,device_name,refresh_hash,refresh_expires_at,id)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,COALESCE(NULLIF($10,'')::uuid,gen_random_uuid()))
        ON CONFLICT(id) DO UPDATE SET token_hash=EXCLUDED.token_hash,refresh_hash=EXCLUDED.refresh_hash,
        expires_at=EXCLUDED.expires_at,refresh_expires_at=EXCLUDED.refresh_expires_at
        RETURNING id`, hash, user, result.ExpiresAt, business, account, generation, name, refreshHash, result.RefreshExpiresAt, existingID).Scan(&result.SessionID)
	return result, err
}

func deviceName(w http.ResponseWriter, name *string) bool {
	*name = strings.TrimSpace(*name)
	if !validText(*name, 128) {
		fail(w, 400, "invalid_device", "Supply a device_name of 1–128 bytes")
		return false
	}
	return true
}

func (a *api) limitedAuth(w http.ResponseWriter, r *http.Request) bool {
	if a.limiter.allow(r.RemoteAddr) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	fail(w, 429, "rate_limited", "Too many authentication attempts; retry in one minute")
	return false
}

func (a *api) startWhatsAppLogin(w http.ResponseWriter, r *http.Request) {
	if !a.limitedAuth(w, r) {
		return
	}
	var input struct {
		Phone string `json:"phone_number"`
		Name  string `json:"device_name"`
	}
	if !decode(w, r, &input) || !deviceName(w, &input.Name) {
		return
	}
	if !phonePattern.MatchString(input.Phone) {
		fail(w, 400, "invalid_phone", "Supply an international phone_number")
		return
	}
	if a.whatsapp == nil {
		fail(w, 503, "whatsapp_unavailable", "WhatsApp login worker is unavailable")
		return
	}
	token, hash, err := newSecret()
	if err != nil {
		dbFailure(w, err)
		return
	}
	// This is a trusted worker operation: public callers cannot bind accounts via the runtime role.
	tx, err := a.whatsapp.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "ipo-login/"+input.Phone); err != nil {
		dbFailure(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM ipo_pos.login_attempts WHERE expires_at<=now()`); err != nil {
		dbFailure(w, err)
		return
	}
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM ipo_pos.login_attempts WHERE phone_number=$1 AND created_at>now()-interval '1 minute'`, input.Phone).Scan(&count); err != nil {
		dbFailure(w, err)
		return
	}
	if count >= 3 {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "rate_limited", "Retry in one minute")
		return
	}
	var user actor
	var account string
	var jid sql.NullString
	err = tx.QueryRowContext(r.Context(), `SELECT u.id,m.business_id,m.role FROM ipo_pos.users u
        JOIN ipo_pos.business_memberships m ON m.user_id=u.id JOIN ipo_pos.businesses b ON b.id=m.business_id
        WHERE u.phone_number=$1 AND u.disabled_at IS NULL AND m.disabled_at IS NULL AND b.archived_at IS NULL
        ORDER BY m.joined_at,m.business_id LIMIT 1`, input.Phone).Scan(&user.UserID, &user.BusinessID, &user.Role)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		dbFailure(w, err)
		return
	}
	if known {
		err = tx.QueryRowContext(r.Context(), `SELECT wa.id,wa.business_id,wa.device_jid FROM ipo_pos.whatsapp_accounts wa
            JOIN ipo_pos.business_memberships m ON m.business_id=wa.business_id AND m.user_id=wa.linked_by_user_id
            JOIN ipo_pos.businesses b ON b.id=m.business_id
            WHERE wa.linked_by_user_id=$1 AND m.disabled_at IS NULL AND b.archived_at IS NULL
            ORDER BY (wa.device_jid IS NOT NULL) DESC,wa.created_at,wa.id LIMIT 1`, user.UserID).Scan(&account, &user.BusinessID, &jid)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(r.Context(), `INSERT INTO ipo_pos.whatsapp_accounts(business_id,label,linked_by_user_id)
                VALUES($1,'POS login',$2) RETURNING id`, user.BusinessID, user.UserID).Scan(&account)
		}
		if err != nil {
			dbFailure(w, err)
			return
		}
		var outstanding bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM ipo_pos.login_attempts WHERE business_id=$1 AND whatsapp_account_id=$2 AND consumed_at IS NULL)`, user.BusinessID, account).Scan(&outstanding); err != nil {
			dbFailure(w, err)
			return
		}
		// Never let a second unauthenticated caller inherit the first caller's pairing proof.
		if outstanding || jid.Valid {
			known = false
		}
	}
	var signupUser, signupBusiness, signupAccount any
	if !known && a.developmentSignup {
		var available bool
		err = tx.QueryRowContext(r.Context(), `SELECT NOT EXISTS(SELECT 1 FROM ipo_pos.users WHERE phone_number=$1)
            AND NOT EXISTS(SELECT 1 FROM ipo_pos.login_attempts WHERE phone_number=$1 AND signup_account_id IS NOT NULL
                AND expires_at>now() AND consumed_at IS NULL)`, input.Phone).Scan(&available)
		if err != nil {
			dbFailure(w, err)
			return
		}
		if available {
			if err = tx.QueryRowContext(r.Context(), `SELECT gen_random_uuid(),gen_random_uuid(),gen_random_uuid()`).Scan(&user.UserID, &user.BusinessID, &account); err != nil {
				dbFailure(w, err)
				return
			}
			signupUser, signupBusiness, signupAccount = user.UserID, user.BusinessID, account
			known = true
		}
	}
	var userArg, businessArg, accountArg any
	if known && signupUser == nil {
		userArg, businessArg, accountArg = user.UserID, user.BusinessID, account
	}
	var expiry time.Time
	if err = tx.QueryRowContext(r.Context(), `INSERT INTO ipo_pos.login_attempts(token_hash,phone_number,user_id,business_id,whatsapp_account_id,device_name,
        signup_user_id,signup_business_id,signup_account_id)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING expires_at`, hash, input.Phone, userArg, businessArg, accountArg, input.Name, signupUser, signupBusiness, signupAccount).Scan(&expiry); err != nil {
		dbFailure(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	if known {
		if _, err = a.whatsapp.start(user, account, "phone", input.Phone, hash); err != nil {
			_, _ = a.whatsapp.db.ExecContext(r.Context(), `DELETE FROM ipo_pos.login_attempts WHERE token_hash=$1`, hash)
			fail(w, 409, "login_busy", "An attempt is already running; wait for it to finish")
			return
		}
	}
	// Pending starts expose neither account IDs nor another caller's pairing proof.
	writeJSON(w, 202, map[string]any{"attempt_token": token, "state": "pending", "expires_at": expiry})
}

func decodeSecret(w http.ResponseWriter, r *http.Request, key string) ([]byte, bool) {
	var input map[string]string
	if !decode(w, r, &input) {
		return nil, false
	}
	if len(input) != 1 {
		fail(w, 400, "invalid_token", "Supply only "+key)
		return nil, false
	}
	hash, ok := secretHash(input[key])
	if !ok {
		fail(w, 401, "invalid_token", "Token is invalid or expired")
		return nil, false
	}
	return hash, true
}

func (a *api) whatsAppLoginStatus(w http.ResponseWriter, r *http.Request) {
	hash, ok := decodeSecret(w, r, "attempt_token")
	if !ok {
		return
	}
	var business, account sql.NullString
	var state string
	err := a.db.QueryRowContext(r.Context(), `SELECT COALESCE(business_id,signup_business_id),COALESCE(whatsapp_account_id,signup_account_id),state FROM ipo_pos.login_attempts
        WHERE token_hash=$1 AND expires_at>now() AND consumed_at IS NULL`, hash).Scan(&business, &account, &state)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "invalid_token", "Token is invalid or expired")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	result := connectionState{State: state}
	if state == "pending" && account.Valid && a.whatsapp != nil {
		if current, found := a.whatsapp.state(business.String, account.String); found {
			result = current
		}
	}
	writeJSON(w, 200, result)
}

func (a *api) exchangeWhatsAppLogin(w http.ResponseWriter, r *http.Request) {
	if !a.limitedAuth(w, r) {
		return
	}
	hash, ok := decodeSecret(w, r, "attempt_token")
	if !ok {
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	var user, business, account, name string
	var generation int64
	err = tx.QueryRowContext(r.Context(), `SELECT user_id,business_id,whatsapp_account_id,device_name,auth_generation
        FROM ipo_pos.login_attempts WHERE token_hash=$1 AND state='verified' AND expires_at>now() AND consumed_at IS NULL`, hash).Scan(&user, &business, &account, &name, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "login_unverified", "Login proof is invalid, incomplete or expired")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	result, err := issueLinkedSession(r.Context(), tx, user, business, account, generation, name, "", nil)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "link_revoked", "Approve a fresh login")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	consumed, err := tx.ExecContext(r.Context(), `UPDATE ipo_pos.login_attempts SET consumed_at=now()
	WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now()`, hash)
	if err != nil {
		dbFailure(w, err)
		return
	}
	if count, err := consumed.RowsAffected(); err != nil {
		dbFailure(w, err)
		return
	} else if count != 1 {
		fail(w, 401, "invalid_token", "Login proof was already consumed")
		return
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (a *api) refreshSession(w http.ResponseWriter, r *http.Request) {
	if !a.limitedAuth(w, r) {
		return
	}
	hash, ok := decodeSecret(w, r, "refresh_token")
	if !ok {
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	var user, business, account, name, id string
	var generation int64
	err = tx.QueryRowContext(r.Context(), `SELECT s.user_id,s.business_id,s.whatsapp_account_id,s.auth_generation,s.device_name,s.id
		FROM ipo_pos.login_sessions s`+sessionJoins+`WHERE s.refresh_hash=$1 AND s.refresh_expires_at>now() AND `+sessionEligible, hash).Scan(&user, &business, &account, &generation, &name, &id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "invalid_token", "Session is invalid or expired")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	// Retain the device/session ID and any outstanding device token while rotating both credentials.
	result, err := issueLinkedSession(r.Context(), tx, user, business, account, generation, name, id, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 401, "link_revoked", "Approve a fresh login")
		} else {
			dbFailure(w, err)
		}
		return
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (a *api) createDeviceToken(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !decode(w, r, &input) {
		return
	}
	token, hash, err := newSecret()
	if err != nil {
		dbFailure(w, err)
		return
	}
	current, _ := sessionHash(r)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	var business, account string
	var generation int64
	err = tx.QueryRowContext(r.Context(), `SELECT s.business_id,s.whatsapp_account_id,s.auth_generation FROM ipo_pos.login_sessions s
	`+sessionJoins+`WHERE s.token_hash=$1 AND s.whatsapp_account_id IS NOT NULL AND s.expires_at>now() AND `+sessionEligible, current).Scan(&business, &account, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 403, "verified_login_required", "Sign in through verified WhatsApp first")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	if err = lockSessionLink(r.Context(), tx, user, business, account, generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 401, "link_revoked", "Approve a fresh login")
		} else {
			dbFailure(w, err)
		}
		return
	}
	var expiry time.Time
	err = tx.QueryRowContext(r.Context(), `INSERT INTO ipo_pos.device_login_tokens AS d
        (token_hash,source_session_id,user_id,business_id,whatsapp_account_id,auth_generation)
        SELECT $1,s.id,s.user_id,s.business_id,s.whatsapp_account_id,s.auth_generation FROM ipo_pos.login_sessions s
        `+sessionJoins+`WHERE s.token_hash=$2 AND s.user_id=$3 AND s.expires_at>now() AND s.whatsapp_account_id IS NOT NULL AND `+sessionEligible+`
        ON CONFLICT(source_session_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,created_at=now(),expires_at=now()+interval '2 minutes'
        RETURNING expires_at`, hash, current, user).Scan(&expiry)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 403, "verified_login_required", "Sign in through verified WhatsApp first")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"device_token": token, "expires_at": expiry})
}

func (a *api) exchangeDeviceToken(w http.ResponseWriter, r *http.Request) {
	if !a.limitedAuth(w, r) {
		return
	}
	var input struct {
		Token string `json:"device_token"`
		Name  string `json:"device_name"`
	}
	if !decode(w, r, &input) || !deviceName(w, &input.Name) {
		return
	}
	hash, ok := secretHash(input.Token)
	if !ok {
		fail(w, 401, "invalid_token", "Token is invalid or expired")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	var user, business, account string
	var generation int64
	err = tx.QueryRowContext(r.Context(), `SELECT d.user_id,d.business_id,d.whatsapp_account_id,d.auth_generation
	FROM ipo_pos.device_login_tokens d JOIN ipo_pos.login_sessions s ON s.id=d.source_session_id
        WHERE d.token_hash=$1 AND d.expires_at>now() AND s.id=d.source_session_id AND s.refresh_expires_at>now()
        `, hash).Scan(&user, &business, &account, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "invalid_token", "Token is invalid, used or expired")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	result, err := issueLinkedSession(r.Context(), tx, user, business, account, generation, input.Name, "", nil)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "link_revoked", "Approve a fresh login")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	consumed, err := tx.ExecContext(r.Context(), `DELETE FROM ipo_pos.device_login_tokens WHERE token_hash=$1 AND expires_at>now()`, hash)
	if err != nil {
		dbFailure(w, err)
		return
	}
	if count, err := consumed.RowsAffected(); err != nil {
		dbFailure(w, err)
		return
	} else if count != 1 {
		fail(w, 401, "invalid_token", "Device token was already consumed or revoked")
		return
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (a *api) listSessions(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	current, _ := sessionHash(r)
	queryJSON(w, r, a.db, 200, `SELECT jsonb_build_object('items',COALESCE(jsonb_agg(to_jsonb(d) ORDER BY d.created_at DESC,d.id),'[]'::jsonb)) FROM
        (SELECT s.id,s.device_name,s.created_at,s.expires_at,s.refresh_expires_at,s.whatsapp_account_id,(s.token_hash=$2) AS current
        FROM ipo_pos.login_sessions s`+sessionJoins+`WHERE s.user_id=$1 AND `+sessionEligible+`
        AND (s.expires_at>now() OR s.refresh_expires_at>now()) ORDER BY s.created_at DESC,s.id LIMIT 100) d`, user, current)
}

func (a *api) removeSession(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validUUID(id) {
		fail(w, 400, "invalid_id", "Invalid session ID")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM ipo_pos.login_sessions WHERE user_id=$1 AND id=$2`, user, id); err != nil {
		dbFailure(w, err)
		return
	}
	w.WriteHeader(204)
}

func (a *api) logoutAll(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM ipo_pos.login_sessions WHERE user_id=$1`, user); err != nil {
		dbFailure(w, err)
		return
	}
	w.WriteHeader(204)
}
