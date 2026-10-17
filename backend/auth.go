package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type actor struct{ UserID, BusinessID, Role string }
type loginWindow struct {
	Count int
	Until time.Time
}
type loginLimiter struct {
	sync.Mutex
	windows map[string]loginWindow
}

func newAPI(db *sql.DB) (*api, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte("dummy-password-for-unknown-accounts"), bcrypt.DefaultCost)
	return &api{db: db, dummyHash: hash, limiter: loginLimiter{windows: make(map[string]loginWindow)}}, err
}

func (l *loginLimiter) allow(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	now := time.Now()
	l.Lock()
	defer l.Unlock()
	window := l.windows[host]
	if now.After(window.Until) {
		// ponytail: bounded per-process limiter; use shared limits when running multiple replicas.
		if len(l.windows) >= 4096 {
			for key, v := range l.windows {
				if now.After(v.Until) {
					delete(l.windows, key)
				}
			}
			if len(l.windows) >= 4096 {
				return false
			}
		}
		window = loginWindow{Until: now.Add(time.Minute)}
	}
	window.Count++
	l.windows[host] = window
	return window.Count <= 10
}

func validPassword(s string) bool { return len(s) >= 12 && len(s) <= 72 }

func (a *api) login(w http.ResponseWriter, r *http.Request) {
	if !a.passwordLogin {
		fail(w, 403, "password_login_disabled", "Use WhatsApp login or a device login token")
		return
	}
	if !a.limitedAuth(w, r) {
		return
	}
	var input struct {
		Phone    string `json:"phone_number"`
		Password string `json:"password"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !phonePattern.MatchString(input.Phone) || len(input.Password) == 0 || len(input.Password) > 72 {
		fail(w, 401, "invalid_credentials", "Invalid phone number or password")
		return
	}
	var id, hash string
	err := a.db.QueryRowContext(r.Context(), `SELECT u.id,c.password_hash FROM ipo_pos.users u
        JOIN ipo_pos.user_credentials c ON c.user_id=u.id
        WHERE u.phone_number=$1 AND u.disabled_at IS NULL`, input.Phone).Scan(&id, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		dbFailure(w, err)
		return
	}
	candidate := []byte(hash)
	if err != nil {
		candidate = a.dummyHash
	}
	matches := bcrypt.CompareHashAndPassword(candidate, []byte(input.Password)) == nil
	if err != nil || !matches {
		fail(w, 401, "invalid_credentials", "Invalid phone number or password")
		return
	}
	token, hashToken, err := newSecret()
	if err != nil {
		fail(w, 500, "internal_error", "Could not create a session")
		return
	}
	expiry := time.Now().UTC().Add(12 * time.Hour)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `SELECT set_config('venweb.auth_reason','session_expired',true),set_config('venweb.auth_release',$1,true)`, a.release); err != nil {
		dbFailure(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM ipo_pos.login_sessions WHERE expires_at<=now() AND (refresh_expires_at IS NULL OR refresh_expires_at<=now())"); err != nil {
		dbFailure(w, err)
		return
	}
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO ipo_pos.login_sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)", hashToken, id, expiry); err != nil {
		dbFailure(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": token, "token_type": "Bearer", "expires_at": expiry, "user_id": id})
}

func newSecret() (string, []byte, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256(bytes)
	return base64.RawURLEncoding.EncodeToString(bytes), hash[:], nil
}

func secretHash(token string) ([]byte, bool) {
	bytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(bytes) != 32 || len(token) != 43 {
		return nil, false
	}
	hash := sha256.Sum256(bytes)
	return hash[:], true
}

func sessionHash(r *http.Request) ([]byte, bool) {
	value := r.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) != 43 {
		return nil, false
	}
	return secretHash(token)
}

const sessionJoins = ` JOIN ipo_pos.users u ON u.id=s.user_id
    LEFT JOIN ipo_pos.whatsapp_accounts wa ON wa.business_id=s.business_id AND wa.id=s.whatsapp_account_id
    LEFT JOIN ipo_pos.business_memberships m ON m.business_id=s.business_id AND m.user_id=s.user_id
    LEFT JOIN ipo_pos.businesses b ON b.id=s.business_id `
const sessionEligible = ` u.disabled_at IS NULL AND (s.whatsapp_account_id IS NULL OR
    (wa.linked_by_user_id=s.user_id AND wa.device_jid IS NOT NULL AND wa.unlinked_at IS NULL
    AND wa.auth_generation=s.auth_generation AND m.disabled_at IS NULL AND m.user_id IS NOT NULL AND b.archived_at IS NULL)) `

func (a *api) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	hash, ok := sessionHash(r)
	if !ok {
		fail(w, 401, "unauthorized", "Sign in with a valid bearer token")
		return "", false
	}
	var userID string
	err := a.db.QueryRowContext(r.Context(), `SELECT s.user_id FROM ipo_pos.login_sessions s`+sessionJoins+`
        WHERE s.token_hash=$1 AND s.expires_at>now() AND `+sessionEligible, hash).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "unauthorized", "Session is invalid or expired")
		return "", false
	}
	if err != nil {
		dbFailure(w, err)
		return "", false
	}
	return userID, true
}

func (a *api) protect(admin bool, next func(http.ResponseWriter, *http.Request, actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		businessID := r.PathValue("businessID")
		if !validUUID(businessID) {
			fail(w, 400, "invalid_id", "Invalid business ID")
			return
		}
		var role string
		err := a.db.QueryRowContext(r.Context(), `SELECT m.role FROM ipo_pos.business_memberships m
            JOIN ipo_pos.businesses b ON b.id=m.business_id
            WHERE m.business_id=$1 AND m.user_id=$2 AND m.disabled_at IS NULL AND b.archived_at IS NULL`, businessID, userID).Scan(&role)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && admin && role != "admin") {
			fail(w, 403, "forbidden", "You do not have access to this operation")
			return
		}
		if err != nil {
			dbFailure(w, err)
			return
		}
		next(w, r, actor{userID, businessID, role})
	}
}

func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticate(w, r); !ok {
		return
	}
	hash, _ := sessionHash(r)
	if err := a.deleteSessions(r.Context(), "logout", "DELETE FROM ipo_pos.login_sessions WHERE token_hash=$1", hash); err != nil {
		dbFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	id, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	var data json.RawMessage
	err := a.db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('user_id',u.id,'name',u.name,'phone_number',u.phone_number,
        'businesses',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',b.id,'name',b.name,'role',m.role) ORDER BY b.name,b.id)
        FROM ipo_pos.business_memberships m JOIN ipo_pos.businesses b ON b.id=m.business_id
        WHERE m.user_id=u.id AND m.disabled_at IS NULL AND b.archived_at IS NULL),'[]'::jsonb))
        FROM ipo_pos.users u WHERE u.id=$1`, id).Scan(&data)
	if err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 200, data)
}
