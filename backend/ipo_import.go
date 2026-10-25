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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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

func openIPOImportDatabase(ctx context.Context, dsn string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid IPO_DATABASE_URL configuration")
	}
	config.Tracer = databaseTrace{pool: "ipo_import"}
	config.ConnectTimeout = 10 * time.Second
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = db.PingContext(readyCtx); err != nil {
		_ = db.Close()
		return nil, errors.New("cannot connect to PostgreSQL; check IPO_DATABASE_URL and server availability")
	}
	return db, nil
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

func runIPOImport(ctx context.Context, db *sql.DB, collect func(context.Context) ([]ipoSourceIssue, map[string]string)) (worked bool, resultErr error) {
	span := startOperation(ctx, "queue.process", "ipo.import")
	defer func() { finishPoll(span, worked, resultErr) }()
	ctx = span.Context()
	acquire := startOperation(ctx, "db.pool.acquire", "ipo.connection_wait")
	conn, err := db.Conn(acquire.Context())
	// Idle importer polls have no standalone timing metrics.
	acquire.Finish()
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
	var created time.Time
	err = conn.QueryRowContext(ctx, `WITH due AS (SELECT id FROM ipo_pos.ipo_import_runs
	 WHERE status IN ('pending','running','failed','partial') AND attempts<3 AND next_attempt_at<=clock_timestamp()
	 ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
	 UPDATE ipo_pos.ipo_import_runs j SET status='running',attempts=attempts+1,started_at=clock_timestamp(),
	 next_attempt_at=clock_timestamp()+interval '10 minutes' FROM due WHERE j.id=due.id RETURNING j.id,j.created_at`).Scan(&id, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	collectCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	span.SetTag("job.key", performanceKey(id))
	recordQueueAge(ctx, "ipo_import", created)
	collectSpan := startOperation(collectCtx, "http.client", "ipo.collect")
	items, results := collect(collectSpan.Context())
	collectErr := collectCtx.Err()
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
	if status != "succeeded" {
		collectSpan.Status = sentry.SpanStatusInternalError
	}
	finishOperation(collectSpan, collectErr)
	saveCtx, saveCancel := context.WithTimeout(ctx, 90*time.Second)
	defer saveCancel()
	save := startOperation(saveCtx, "db.persist", "ipo.persist")
	var saveErr error
	for first := 0; first < len(items); first += 100 {
		var saved int
		saved, err = persistSourceIPOs(save.Context(), db, items[first:min(first+100, len(items))], results)
		count += saved
		if err != nil {
			saveErr = err
			status = "partial"
			if saveCtx.Err() != nil {
				break
			}
		}
	}
	finishOperation(save, saveErr)
	if count == 0 {
		status = "failed"
	}
	encoded, _ := json.Marshal(results)
	span.SetData("import.outcome", status)
	if status != "succeeded" {
		span.Status = sentry.SpanStatusInternalError
	}
	span.SetData("import.count", count)
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
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

func persistSourceIPOs(ctx context.Context, db *sql.DB, items []ipoSourceIssue, results map[string]string) (int, error) {
	err := saveSourceIPOs(ctx, db, items)
	if err == nil {
		return len(items), nil
	}
	if len(items) == 1 || ctx.Err() != nil {
		for _, item := range items {
			results["storage:"+item.Symbol] = "failed"
		}
		return 0, err
	}
	// Split a rolled-back batch so one rejected issue cannot block valid issues.
	middle := len(items) / 2
	left, leftErr := persistSourceIPOs(ctx, db, items[:middle], results)
	right, rightErr := persistSourceIPOs(ctx, db, items[middle:], results)
	return left + right, errors.Join(leftErr, rightErr)
}

const sourceIPORows = `WITH source AS (
 SELECT s.*,i.id FROM jsonb_to_recordset($1::jsonb) AS s(
 "Symbol" text,"Name" text,"Board" text,"SourceURL" text,"SourceStatus" text,
 "Open" text,"Close" text,"Listing" text,"Low" text,"High" text,"Lot" bigint,
 "Registrar" text,"IssueText" text,"IssueShares" text,"IssueAmount" text,
 "DetailOK" boolean,"EnrichmentOK" boolean,"EnrichmentURL" text,"Timeline" jsonb,
 "Subscriptions" jsonb,"Reservations" jsonb,"Shareholdings" jsonb,"Lockins" jsonb)
 LEFT JOIN ipo_pos.ipos i ON i.catalogue_source='nse' AND i.source_issue_id=s."Symbol") `

func saveSourceIPOs(ctx context.Context, db *sql.DB, items []ipoSourceIssue) error {
	if len(items) == 0 {
		return nil
	}
	type sourceRow struct {
		ipoSourceIssue
		SourceURL string
	}
	rows := make([]sourceRow, len(items))
	for n, item := range items {
		if !sourceSymbolPattern.MatchString(item.Symbol) || !validText(item.Name, 256) || (item.Board != "SME" && item.Board != "Mainboard") {
			return errors.New("invalid source identity")
		}
		rows[n] = sourceRow{item, "https://www.nseindia.com/market-data/issue-information?symbol=" + url.QueryEscape(item.Symbol)}
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Only provider-owned rows match this key. Manual products are never joined by name.
	result, err := tx.ExecContext(ctx, sourceIPORows+`INSERT INTO ipo_pos.ipos AS i(code,name,issuer_name,opens_on,closes_on,listing_on,price_low,price_high,
	 catalogue_source,source_issue_id,symbol,exchange,board,source_url,last_seen_at,source_status,issue_size_text)
	 SELECT 'NSE:'||"Symbol","Name","Name","Open"::date,"Close"::date,"Listing"::date,"Low"::numeric,"High"::numeric,
	 'nse',"Symbol","Symbol",'NSE',"Board","SourceURL",clock_timestamp(),"SourceStatus",NULLIF("IssueText",'') FROM source WHERE true
	 ON CONFLICT(catalogue_source,source_issue_id) DO UPDATE SET name=excluded.name,issuer_name=excluded.issuer_name,
	 opens_on=COALESCE(excluded.opens_on,i.opens_on),closes_on=COALESCE(excluded.closes_on,i.closes_on),
	 listing_on=COALESCE(excluded.listing_on,i.listing_on),price_low=COALESCE(excluded.price_low,i.price_low),
	 price_high=COALESCE(excluded.price_high,i.price_high),board=excluded.board,last_seen_at=excluded.last_seen_at,
	 source_status=COALESCE(excluded.source_status,i.source_status),issue_size_text=COALESCE(i.issue_size_text,excluded.issue_size_text)
	 WHERE i.name=excluded.name AND (i.closes_on IS NULL OR excluded.opens_on IS NULL OR excluded.opens_on<=i.closes_on+interval '90 days')
	 `, string(encoded))
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != int64(len(items)) {
		return sql.ErrNoRows
	}
	for _, query := range []string{
		`UPDATE ipo_pos.ipos i SET lot_size=COALESCE(NULLIF(s."Lot",0),i.lot_size),
		 registrar=COALESCE(NULLIF(s."Registrar",''),i.registrar),issue_size_text=COALESCE(NULLIF(s."IssueText",''),i.issue_size_text),
		 issue_size_shares=COALESCE(s."IssueShares"::numeric,i.issue_size_shares),terms_observed_at=clock_timestamp()
		 FROM source s WHERE i.id=s.id AND s."DetailOK"`,
		`INSERT INTO ipo_pos.ipo_subscriptions(ipo_id,category,label,source,offered_shares,bid_shares,applications,multiple,observed_at)
		 SELECT s.id,x."Category",x."Label",'nse',x."Offered",x."Bid",x."Applications",x."Multiple",clock_timestamp()
		 FROM source s CROSS JOIN LATERAL jsonb_to_recordset(s."Subscriptions") AS x(
		 "Category" text,"Label" text,"Offered" numeric,"Bid" numeric,"Applications" numeric,"Multiple" numeric)
		 ON CONFLICT(ipo_id,category) DO UPDATE SET label=excluded.label,
		 offered_shares=COALESCE(excluded.offered_shares,ipo_subscriptions.offered_shares),
		 bid_shares=COALESCE(excluded.bid_shares,ipo_subscriptions.bid_shares),applications=COALESCE(excluded.applications,ipo_subscriptions.applications),
		 multiple=COALESCE(excluded.multiple,ipo_subscriptions.multiple),observed_at=excluded.observed_at`,
		`UPDATE ipo_pos.ipos i SET timeline=i.timeline||s."Timeline" FROM source s
		 WHERE i.id=s.id AND s."Timeline"<>'{}'::jsonb`,
		`UPDATE ipo_pos.ipos i SET issue_size_amount=COALESCE(s."IssueAmount"::numeric,i.issue_size_amount)
		 FROM source s WHERE i.id=s.id AND s."EnrichmentOK"`,
		`INSERT INTO ipo_pos.ipo_reservations(ipo_id,category,reserved_shares,percentage,basis,source_url,observed_at)
		 SELECT DISTINCT ON (s.id,x."Category") s.id,x."Category",x."Shares",x."Percentage",x."Basis",s."EnrichmentURL",clock_timestamp()
		 FROM source s CROSS JOIN LATERAL jsonb_array_elements(s."Reservations") WITH ORDINALITY AS entry(value,position)
		 CROSS JOIN LATERAL jsonb_to_record(entry.value) AS x("Category" text,"Shares" numeric,"Percentage" numeric,"Basis" text)
		 WHERE s."EnrichmentOK" ORDER BY s.id,x."Category",entry.position DESC
		 ON CONFLICT(ipo_id,category) DO UPDATE SET reserved_shares=excluded.reserved_shares,
		 percentage=excluded.percentage,basis=excluded.basis,source_url=excluded.source_url,observed_at=excluded.observed_at`,
		`INSERT INTO ipo_pos.ipo_promoter_shareholdings(ipo_id,holder,pre_percentage,post_percentage,source_url,observed_at)
		 SELECT DISTINCT ON (s.id,x."Holder") s.id,x."Holder",x."Pre",x."Post",s."EnrichmentURL",clock_timestamp()
		 FROM source s CROSS JOIN LATERAL jsonb_array_elements(s."Shareholdings") WITH ORDINALITY AS entry(value,position)
		 CROSS JOIN LATERAL jsonb_to_record(entry.value) AS x("Holder" text,"Pre" numeric,"Post" numeric)
		 WHERE s."EnrichmentOK" ORDER BY s.id,x."Holder",entry.position DESC
		 ON CONFLICT(ipo_id,holder) DO UPDATE SET pre_percentage=excluded.pre_percentage,
		 post_percentage=excluded.post_percentage,source_url=excluded.source_url,observed_at=excluded.observed_at`,
		`INSERT INTO ipo_pos.ipo_anchor_lockins(ipo_id,label,expires_on,source_url,observed_at)
		 SELECT DISTINCT ON (s.id,x."Label") s.id,x."Label",x."Date",s."EnrichmentURL",clock_timestamp()
		 FROM source s CROSS JOIN LATERAL jsonb_array_elements(s."Lockins") WITH ORDINALITY AS entry(value,position)
		 CROSS JOIN LATERAL jsonb_to_record(entry.value) AS x("Label" text,"Date" date)
		 WHERE s."EnrichmentOK" ORDER BY s.id,x."Label",entry.position DESC
		 ON CONFLICT(ipo_id,label) DO UPDATE SET expires_on=excluded.expires_on,
		 source_url=excluded.source_url,observed_at=excluded.observed_at`,
		`SELECT ipo_pos.capture_ipo_dashboard_basis(id) FROM source ORDER BY id`,
	} {
		if _, err = tx.ExecContext(ctx, sourceIPORows+query, string(encoded)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
