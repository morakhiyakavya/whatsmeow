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

	"github.com/getsentry/sentry-go"
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
					workCtx, stop = context.WithTimeout(ctx, 15*time.Second)
					directoryFound, directoryErr := deliverDirectoryPush(workCtx, db, sender)
					stop()
					if err != nil || directoryErr != nil {
						log.Print("Contact notification storage unavailable")
					}
					if err != nil || directoryErr != nil || (!found && !directoryFound) {
						break
					}
				}
			}
		}
	}()
	return func() { cancel(); wg.Wait() }
}

func deliverProfilePush(ctx context.Context, db *sql.DB, sender pushSender) (worked bool, resultErr error) {
	return deliverContactPush(ctx, db, sender, false)
}

func deliverDirectoryPush(ctx context.Context, db *sql.DB, sender pushSender) (bool, error) {
	return deliverContactPush(ctx, db, sender, true)
}

func deliverContactPush(ctx context.Context, db *sql.DB, sender pushSender, directory bool) (worked bool, resultErr error) {
	table, revisionColumn := "ipo_pos.profile_push_outbox", "profile_revision"
	fields := "q.whatsapp_account_id,q.phone_number,q.profile_revision,q.auth_generation"
	if directory {
		table, revisionColumn = "ipo_pos.directory_push_outbox", "revision"
		fields = "''::text,''::text,q.revision,0::bigint"
	}
	span := startOperation(ctx, "queue.process", "profile_push.deliver")
	span.SetData("push.directory", directory)
	defer func() { finishPoll(span, worked, resultErr) }()
	ctx = span.Context()
	// Remove terminal work, even across worker restarts.
	_, err := db.ExecContext(ctx, `DELETE FROM `+table+` WHERE expires_at<=now() OR
		(attempts>=5 AND next_attempt_at<=now())`)
	if err != nil {
		return false, err
	}
	var job profilePushJob
	var due time.Time
	err = db.QueryRowContext(ctx, `WITH due AS (
		SELECT id,next_attempt_at FROM `+table+` WHERE next_attempt_at<=now() AND attempts<5 AND expires_at>now()
		ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE `+table+` q SET attempts=q.attempts+1,next_attempt_at=clock_timestamp()+interval '1 minute'
	FROM due WHERE q.id=due.id RETURNING q.id,q.installation_id,q.device_generation,q.user_id,q.business_id,
		`+fields+`,q.attempts,due.next_attempt_at`).Scan(
		&job.id, &job.installation, &job.deviceGeneration, &job.user, &job.business, &job.account, &job.phone,
		&job.revision, &job.authGeneration, &job.attempts, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var token string
	span.SetTag("job.key", performanceKey(job.id))
	span.SetTag("worker.key", performanceKey(job.business+"/"+job.account))
	queue := "profile_push"
	if directory {
		queue = "directory_push"
	}
	recordQueueAge(ctx, queue, due)
	// Recheck session, installation generation, link ownership and business access immediately before sending.
	if directory {
		err = db.QueryRowContext(ctx, `SELECT p.fcm_token FROM ipo_pos.push_devices p
			JOIN ipo_pos.login_sessions s ON s.id=p.session_id `+sessionJoins+`
			JOIN ipo_pos.business_memberships member ON member.business_id=$4 AND member.user_id=p.user_id
			JOIN ipo_pos.businesses business ON business.id=member.business_id
			WHERE p.installation_id=$1 AND p.generation=$2 AND p.user_id=$3
			AND member.disabled_at IS NULL AND business.archived_at IS NULL
			AND (s.business_id IS NULL OR s.business_id=$4) AND COALESCE(s.refresh_expires_at,s.expires_at)>now()
			AND `+sessionEligible, job.installation, job.deviceGeneration, job.user, job.business).Scan(&token)
	} else {
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
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return true, err
	}
	outcome := pushOutcome{}
	if err == nil {
		send := startOperation(ctx, "http.client", "profile_push.send")
		data := map[string]string{
			"type": "contact_profile_changed", "user_id": job.user, "business_id": job.business,
			"whatsapp_account_id": job.account, "phone_number": job.phone,
			"profile_revision": strconv.FormatInt(job.revision, 10),
		}
		if directory {
			data = map[string]string{"type": "contacts_changed", "user_id": job.user,
				"business_id": job.business, "directory_revision": strconv.FormatInt(job.revision, 10)}
		}
		outcome = sender.send(send.Context(), token, data)
		var sendErr error
		if outcome.failed {
			sendErr = errors.New("push send failed")
		}
		if ctx.Err() != nil {
			sendErr = ctx.Err()
		}
		finishOperation(send, sendErr)
		if outcome.failed {
			span.Status = sentry.SpanStatusInternalError
		}
	}
	span.SetData("push.retry", outcome.retry)
	span.SetData("push.unregistered", outcome.unregistered)
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
		_, err = db.ExecContext(ctx, `UPDATE `+table+` SET next_attempt_at=$4
			WHERE id=$1 AND `+revisionColumn+`=$2 AND device_generation=$3`,
			job.id, job.revision, job.deviceGeneration, time.Now().Add(delay))
	} else {
		_, err = db.ExecContext(ctx, `DELETE FROM `+table+` WHERE id=$1 AND `+revisionColumn+`=$2 AND device_generation=$3`,
			job.id, job.revision, job.deviceGeneration)
	}
	if outcome.failed && (!outcome.retry || job.attempts >= 5) && !outcome.unregistered {
		log.Print("Contact notification failed permanently or exhausted retries; registration retained")
	}
	return true, err
}

func profilePushDelay(attempt int, retryAfter time.Duration) time.Duration {
	delay := time.Minute * time.Duration(1<<min(max(attempt-1, 0), 4))
	delay += time.Duration(rand.Int64N(int64(delay / 4)))
	return max(delay, retryAfter)
}
