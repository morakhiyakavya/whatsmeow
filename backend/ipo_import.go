package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

func (a *api) triggerIPORefresh(w http.ResponseWriter, r *http.Request) {
	want := sha256.Sum256([]byte(a.ipoJobToken))
	got := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
	if a.ipoJobToken == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		fail(w, 401, "unauthorized", "A valid job credential is required")
		return
	}
	if r.ContentLength != 0 {
		fail(w, 400, "invalid_request", "Send an empty request body")
		return
	}
	var data json.RawMessage
	slot := time.Now().UTC().Truncate(30 * time.Minute)
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO ipo_pos.ipo_import_runs(scheduled_for)
	 VALUES($1)
	 ON CONFLICT(scheduled_for) DO NOTHING RETURNING jsonb_build_object('id',id,'status',status,'scheduled_for',scheduled_for)`, slot).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		err = a.db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('id',id,'status',status,'scheduled_for',scheduled_for)
		 FROM ipo_pos.ipo_import_runs WHERE scheduled_for=$1`, slot).Scan(&data)
	}
	if err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, 202, data)
}

func (a *api) ipoImportStatus(w http.ResponseWriter, r *http.Request, _ actor) {
	queryJSON(w, r, a.db, 200, `SELECT jsonb_build_object(
	 'latest_run',(SELECT to_jsonb(j) FROM ipo_pos.ipo_import_runs j ORDER BY j.created_at DESC LIMIT 1),
	 'last_successful_refresh',(SELECT max(finished_at) FROM ipo_pos.ipo_import_runs WHERE status='succeeded'),
	 'last_partial_refresh',(SELECT max(finished_at) FROM ipo_pos.ipo_import_runs WHERE status='partial'),
	 'coverage','NSE only; BSE-only coverage unverified',
	 'stale',NOT EXISTS(SELECT 1 FROM ipo_pos.ipo_import_runs WHERE status IN ('succeeded','partial') AND finished_at>now()-interval '90 minutes'))`)
}

func startIPOImporter(ctx context.Context, db *sql.DB) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	client := newIPOSourceClient()
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			if _, err := runIPOImport(ctx, db, client.collect); err != nil && ctx.Err() == nil {
				log.Print("IPO importer database operation failed")
				sentry.CaptureException(errors.New("IPO importer database operation failed"))
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}

func runIPOImport(ctx context.Context, db *sql.DB, collect func(context.Context) ([]ipoSourceIssue, map[string]string)) (bool, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(73195621)`).Scan(&locked); err != nil || !locked {
		return false, err
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(73195621)`); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if _, err = conn.ExecContext(ctx, `UPDATE ipo_pos.ipo_import_runs SET status='failed',finished_at=clock_timestamp(),
	 source_results=source_results||'{"recovery":"attempts_exhausted"}'::jsonb
	 WHERE status='running' AND attempts>=3 AND next_attempt_at<=clock_timestamp()`); err != nil {
		return false, err
	}
	var id string
	err = conn.QueryRowContext(ctx, `WITH due AS (SELECT id FROM ipo_pos.ipo_import_runs
	 WHERE status IN ('pending','running','failed','partial') AND attempts<3 AND next_attempt_at<=clock_timestamp()
	 ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
	 UPDATE ipo_pos.ipo_import_runs j SET status='running',attempts=attempts+1,started_at=clock_timestamp(),
	 next_attempt_at=clock_timestamp()+interval '10 minutes' FROM due WHERE j.id=due.id RETURNING j.id`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	collectCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	items, results := collect(collectCtx)
	interrupted := collectCtx.Err() != nil
	cancel()
	if results == nil {
		results = map[string]string{}
	}
	if interrupted {
		results["deadline"] = "failed"
	}
	status := "succeeded"
	for key, result := range results {
		if key != "coverage" && result != "ok" {
			status = "partial"
		}
	}
	if len(items) == 0 {
		status = "failed"
		results["catalogue"] = "empty"
	}
	count := 0
	saveCtx, saveCancel := context.WithTimeout(ctx, 90*time.Second)
	defer saveCancel()
	for _, item := range items {
		if err = saveSourceIPO(saveCtx, db, item); err != nil {
			status = "partial"
			results["storage:"+item.Symbol] = "failed"
			if saveCtx.Err() != nil {
				break
			}
			continue
		}
		count++
	}
	if count == 0 {
		status = "failed"
	}
	encoded, _ := json.Marshal(results)
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer finishCancel()
	_, err = conn.ExecContext(finishCtx, `UPDATE ipo_pos.ipo_import_runs SET status=$2,finished_at=clock_timestamp(),
	 next_attempt_at=clock_timestamp()+interval '5 minutes',source_results=$3,imported_count=$4 WHERE id=$1`, id, status, string(encoded), count)
	if err == nil {
		log.Printf("IPO import %s: %d issues", status, count)
		if status != "succeeded" {
			sentry.CaptureException(errors.New("IPO catalogue refresh incomplete; inspect protected import status"))
		}
	}
	return true, err
}

func saveSourceIPO(ctx context.Context, db *sql.DB, item ipoSourceIssue) error {
	if !sourceSymbolPattern.MatchString(item.Symbol) || !validText(item.Name, 256) || (item.Board != "SME" && item.Board != "Mainboard") {
		return errors.New("invalid source identity")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sourceURL := "https://www.nseindia.com/market-data/issue-information?symbol=" + url.QueryEscape(item.Symbol)
	var id string
	// Only provider-owned rows match this key. Manual products are never joined by name.
	err = tx.QueryRowContext(ctx, `INSERT INTO ipo_pos.ipos AS i(code,name,issuer_name,opens_on,closes_on,listing_on,price_low,price_high,
	 catalogue_source,source_issue_id,symbol,exchange,board,source_url,last_seen_at,source_status,issue_size_text)
	 VALUES($1,$2,$2,$3,$4,$5,$6,$7,'nse',$8,$8,'NSE',$9,$10,clock_timestamp(),$11,NULLIF($12,''))
	 ON CONFLICT(catalogue_source,source_issue_id) DO UPDATE SET name=excluded.name,issuer_name=excluded.issuer_name,
	 opens_on=COALESCE(excluded.opens_on,i.opens_on),closes_on=COALESCE(excluded.closes_on,i.closes_on),
	 listing_on=COALESCE(excluded.listing_on,i.listing_on),price_low=COALESCE(excluded.price_low,i.price_low),
	 price_high=COALESCE(excluded.price_high,i.price_high),board=excluded.board,last_seen_at=excluded.last_seen_at,
	 source_status=COALESCE(excluded.source_status,i.source_status),issue_size_text=COALESCE(i.issue_size_text,excluded.issue_size_text)
	 WHERE i.name=excluded.name AND (i.closes_on IS NULL OR excluded.opens_on IS NULL OR excluded.opens_on<=i.closes_on+interval '90 days')
	 RETURNING id`, "NSE:"+item.Symbol, item.Name, item.Open, item.Close, item.Listing, item.Low, item.High, item.Symbol, item.Board, sourceURL, item.SourceStatus, item.IssueText).Scan(&id)
	if err != nil {
		return err
	}
	if item.DetailOK {
		_, err = tx.ExecContext(ctx, `UPDATE ipo_pos.ipos SET lot_size=COALESCE(NULLIF($2::bigint,0),lot_size),
		 registrar=COALESCE(NULLIF($3::text,''),registrar),issue_size_text=COALESCE(NULLIF($4::text,''),issue_size_text),
		 issue_size_shares=COALESCE($5::numeric,issue_size_shares),terms_observed_at=clock_timestamp() WHERE id=$1`, id, item.Lot, item.Registrar, item.IssueText, item.IssueShares)
		if err != nil {
			return err
		}
	}
	for _, s := range item.Subscriptions {
		_, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.ipo_subscriptions(ipo_id,category,label,source,offered_shares,bid_shares,applications,multiple,observed_at)
		 VALUES($1,$2,$3,'nse',$4,$5,$6,$7,clock_timestamp()) ON CONFLICT(ipo_id,category) DO UPDATE SET label=excluded.label,
		 offered_shares=COALESCE(excluded.offered_shares,ipo_subscriptions.offered_shares),
		 bid_shares=COALESCE(excluded.bid_shares,ipo_subscriptions.bid_shares),applications=COALESCE(excluded.applications,ipo_subscriptions.applications),
		 multiple=COALESCE(excluded.multiple,ipo_subscriptions.multiple),observed_at=excluded.observed_at`, id, s.Category, s.Label, s.Offered, s.Bid, s.Applications, s.Multiple)
		if err != nil {
			return err
		}
	}
	if len(item.Timeline) > 0 {
		timeline, _ := json.Marshal(item.Timeline)
		if _, err = tx.ExecContext(ctx, `UPDATE ipo_pos.ipos SET timeline=timeline||$2::jsonb WHERE id=$1`, id, string(timeline)); err != nil {
			return err
		}
	}
	if item.EnrichmentOK {
		_, err = tx.ExecContext(ctx, `UPDATE ipo_pos.ipos SET issue_size_amount=COALESCE($2::numeric,issue_size_amount) WHERE id=$1`, id, item.IssueAmount)
		if err != nil {
			return err
		}
		for _, s := range item.Reservations {
			_, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.ipo_reservations(ipo_id,category,reserved_shares,percentage,basis,source_url,observed_at)
			 VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()) ON CONFLICT(ipo_id,category) DO UPDATE SET reserved_shares=excluded.reserved_shares,
			 percentage=excluded.percentage,basis=excluded.basis,source_url=excluded.source_url,observed_at=excluded.observed_at`, id, s.Category, s.Shares, s.Percentage, s.Basis, item.EnrichmentURL)
			if err != nil {
				return err
			}
		}
		for _, s := range item.Shareholdings {
			_, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.ipo_promoter_shareholdings(ipo_id,holder,pre_percentage,post_percentage,source_url,observed_at)
			 VALUES($1,$2,$3,$4,$5,clock_timestamp()) ON CONFLICT(ipo_id,holder) DO UPDATE SET pre_percentage=excluded.pre_percentage,
			 post_percentage=excluded.post_percentage,source_url=excluded.source_url,observed_at=excluded.observed_at`, id, s.Holder, s.Pre, s.Post, item.EnrichmentURL)
			if err != nil {
				return err
			}
		}
		for _, s := range item.Lockins {
			_, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.ipo_anchor_lockins(ipo_id,label,expires_on,source_url,observed_at)
			 VALUES($1,$2,$3,$4,clock_timestamp()) ON CONFLICT(ipo_id,label) DO UPDATE SET expires_on=excluded.expires_on,
			 source_url=excluded.source_url,observed_at=excluded.observed_at`, id, s.Label, s.Date, item.EnrichmentURL)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
