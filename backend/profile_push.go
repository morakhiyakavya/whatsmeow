package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"
)

type profilePushJob struct {
	id, installation, deviceGeneration, user, business, account, phone string
	revision, authGeneration                                           int64
	attempts                                                           int
}

type pushSender interface {
	send(context.Context, string, map[string]string) pushOutcome
}

func startProfilePush(ctx context.Context, db *sql.DB, sender pushSender) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Bound each sweep; new jobs and expired leases are picked up on later ticks.
				for range 32 {
					workCtx, stop := context.WithTimeout(ctx, 15*time.Second)
					found, err := deliverProfilePush(workCtx, db, sender)
					stop()
					if err != nil {
						log.Print("Contact profile notification storage unavailable")
					}
					if err != nil || !found {
						break
					}
				}
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}

func deliverProfilePush(ctx context.Context, db *sql.DB, sender pushSender) (bool, error) {
	// Remove terminal work, even across worker restarts.
	_, err := db.ExecContext(ctx, `DELETE FROM ipo_pos.profile_push_outbox WHERE expires_at<=now() OR
		(attempts>=5 AND next_attempt_at<=now())`)
	if err != nil {
		return false, err
	}
	var job profilePushJob
	err = db.QueryRowContext(ctx, `WITH due AS (
		SELECT id FROM ipo_pos.profile_push_outbox WHERE next_attempt_at<=now() AND attempts<5 AND expires_at>now()
		ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE ipo_pos.profile_push_outbox q SET attempts=q.attempts+1,next_attempt_at=clock_timestamp()+interval '1 minute'
	FROM due WHERE q.id=due.id RETURNING q.id,q.installation_id,q.device_generation,q.user_id,q.business_id,
	q.whatsapp_account_id,q.phone_number,q.profile_revision,q.auth_generation,q.attempts`).Scan(
		&job.id, &job.installation, &job.deviceGeneration, &job.user, &job.business, &job.account, &job.phone,
		&job.revision, &job.authGeneration, &job.attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var token string
	// Recheck session, installation generation, link ownership and business access immediately before sending.
	err = db.QueryRowContext(ctx, `SELECT p.fcm_token FROM ipo_pos.push_devices p
		JOIN ipo_pos.login_sessions s ON s.id=p.session_id `+sessionJoins+`
		JOIN ipo_pos.whatsapp_accounts target ON target.business_id=$4 AND target.id=$5
		JOIN ipo_pos.business_memberships member ON member.business_id=target.business_id AND member.user_id=p.user_id
		JOIN ipo_pos.businesses business ON business.id=target.business_id
		WHERE p.installation_id=$1 AND p.generation=$2 AND p.user_id=$3
		AND target.linked_by_user_id=p.user_id AND target.auth_generation=$6 AND target.device_jid IS NOT NULL
		AND target.unlinked_at IS NULL AND member.disabled_at IS NULL AND business.archived_at IS NULL
		AND COALESCE(s.refresh_expires_at,s.expires_at)>now() AND `+sessionEligible,
		job.installation, job.deviceGeneration, job.user, job.business, job.account, job.authGeneration).Scan(&token)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return true, err
	}
	outcome := pushOutcome{}
	if err == nil {
		outcome = sender.send(ctx, token, map[string]string{
			"type": "contact_profile_changed", "user_id": job.user, "business_id": job.business,
			"whatsapp_account_id": job.account, "phone_number": job.phone,
			"profile_revision": strconv.FormatInt(job.revision, 10),
		})
	}
	if outcome.unregistered {
		// Do not remove a new registration installed while this request was in flight.
		_, err = db.ExecContext(ctx, `DELETE FROM ipo_pos.push_devices WHERE installation_id=$1 AND generation=$2 AND fcm_token=$3`,
			job.installation, job.deviceGeneration, token)
		if err != nil {
			return true, err
		}
	}
	if outcome.retry && job.attempts < 5 {
		delay := profilePushDelay(job.attempts, outcome.after)
		_, err = db.ExecContext(ctx, `UPDATE ipo_pos.profile_push_outbox SET next_attempt_at=$4
			WHERE id=$1 AND profile_revision=$2 AND device_generation=$3`,
			job.id, job.revision, job.deviceGeneration, time.Now().Add(delay))
	} else {
		_, err = db.ExecContext(ctx, `DELETE FROM ipo_pos.profile_push_outbox WHERE id=$1 AND profile_revision=$2 AND device_generation=$3`,
			job.id, job.revision, job.deviceGeneration)
	}
	if outcome.failed && (!outcome.retry || job.attempts >= 5) && !outcome.unregistered {
		log.Print("Contact profile notification failed permanently or exhausted retries; registration retained")
	}
	return true, err
}

func profilePushDelay(attempt int, retryAfter time.Duration) time.Duration {
	delay := time.Minute * time.Duration(1<<min(max(attempt-1, 0), 4))
	delay += time.Duration(rand.Int64N(int64(delay / 4)))
	return max(delay, retryAfter)
}
