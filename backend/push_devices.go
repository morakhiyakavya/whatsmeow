package main

import (
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode"
)

func validFCMToken(token string) bool {
	return len(token) > 0 && len(token) <= 4096 && !strings.ContainsFunc(token, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

func (a *api) registerPushDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := a.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("installationID")
	if !validUUID(id) {
		fail(w, 400, "invalid_id", "Supply an installation UUID")
		return
	}
	var input struct {
		Token string `json:"fcm_token"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !validFCMToken(input.Token) {
		fail(w, 400, "invalid_fcm_token", "Supply a nonempty FCM token of at most 4096 bytes without whitespace")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbFailure(w, err)
		return
	}
	defer tx.Rollback()
	hash, _ := sessionHash(r)
	var session string
	var business, account sql.NullString
	var generation sql.NullInt64
	err = tx.QueryRowContext(r.Context(), `SELECT s.business_id,s.whatsapp_account_id,s.auth_generation
		FROM ipo_pos.login_sessions s`+sessionJoins+` WHERE s.token_hash=$1 AND s.user_id=$2
		AND s.expires_at>now() AND `+sessionEligible, hash, user).Scan(&business, &account, &generation)
	// Match renewal/unlink lock ordering: binding and membership before the session/device.
	if err == nil && account.Valid {
		err = lockSessionLink(r.Context(), tx, user, business.String, account.String, generation.Int64)
	}
	if err == nil {
		err = tx.QueryRowContext(r.Context(), `SELECT s.id FROM ipo_pos.login_sessions s`+sessionJoins+`
			WHERE s.token_hash=$1 AND s.user_id=$2 AND s.expires_at>now() AND `+sessionEligible+` FOR SHARE OF s,u`, hash, user).Scan(&session)
	}
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 401, "unauthorized", "Session is invalid or expired")
		return
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	// Serialize concurrent token/installation replacement in a consistent order.
	keys := []string{"push-installation/" + strings.ToLower(id), "push-token/" + input.Token}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			dbFailure(w, err)
			return
		}
	}
	// Lock both affected rows in UUID order, including when two installations swap tokens.
	_, err = tx.ExecContext(r.Context(), `SELECT installation_id FROM ipo_pos.push_devices
		WHERE installation_id=$1 OR fcm_token=$2 ORDER BY installation_id FOR UPDATE`, id, input.Token)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `DELETE FROM ipo_pos.push_devices WHERE fcm_token=$1 AND installation_id<>$2`, input.Token, id)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO ipo_pos.push_devices AS p(installation_id,fcm_token,user_id,session_id)
			VALUES($1,$2,$3,$4) ON CONFLICT(installation_id) DO UPDATE SET
			fcm_token=EXCLUDED.fcm_token,user_id=EXCLUDED.user_id,session_id=EXCLUDED.session_id,
			generation=CASE WHEN (p.fcm_token,p.user_id,p.session_id) IS DISTINCT FROM
				(EXCLUDED.fcm_token,EXCLUDED.user_id,EXCLUDED.session_id) THEN gen_random_uuid() ELSE p.generation END,
			updated_at=clock_timestamp()`, id, input.Token, user, session)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
