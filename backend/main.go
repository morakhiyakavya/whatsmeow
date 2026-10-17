package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type api struct {
	db                *sql.DB
	limiter           loginLimiter
	dummyHash         []byte
	whatsapp          *whatsAppService
	passwordLogin     bool
	developmentSignup bool
	release           string
}

func main() {
	envPath := flag.String("env", "../.env", "local configuration file")
	bootstrap := flag.Bool("bootstrap", false, "create the initial business/admin, then exit")
	flag.Parse()
	if err := run(*envPath, *bootstrap); err != nil {
		// Driver errors and connection URLs may contain credentials.
		log.Print(err)
		os.Exit(1)
	}
}

func run(envPath string, bootstrap bool) error {
	config, err := loadConfig(envPath)
	if err != nil {
		return err
	}
	if err = sentry.Init(sentryOptions(config)); err != nil {
		return errors.New("invalid Sentry configuration; check SENTRY_DSN")
	}
	defer sentry.Flush(2 * time.Second)
	db, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = db.PingContext(readyCtx)
	cancel()
	if err != nil {
		return errors.New("cannot connect to PostgreSQL; check DATABASE_URL and server availability")
	}
	if bootstrap {
		return bootstrapAdmin(ctx, db, config)
	}
	var schemaReady bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('ipo_pos.login_attempts') IS NOT NULL
		AND to_regclass('ipo_pos.contact_profile_revisions') IS NOT NULL AND to_regclass('ipo_pos.profile_push_outbox') IS NOT NULL
		AND to_regclass('ipo_pos.push_devices') IS NOT NULL
		AND to_regclass('ipo_pos.auth_events') IS NOT NULL
		AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='ipo_pos' AND table_name='login_attempts' AND column_name='recovery_hash')
        AND to_regclass('ipo_pos.device_login_tokens') IS NOT NULL AND to_regclass('ipo_pos.whatsapp_number_checks') IS NOT NULL
		AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='ipo_pos' AND table_name='login_attempts' AND column_name='signup_account_id')
        AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('ipo_pos.transactions') AND conname='transactions_nonzero_quantity_check')
        AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('ipo_pos.transaction_versions') AND conname='transaction_versions_nonzero_quantity_check')
        AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('ipo_pos.transactions') AND conname='transactions_signed_rate_check')
        AND EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('ipo_pos.transaction_versions') AND conname='transaction_versions_signed_rate_check')`).Scan(&schemaReady); err != nil || !schemaReady {
		return errors.New("apply application migrations through database/012_account_recovery.sql before starting the API")
	}
	app, err := newAPI(db)
	if err != nil {
		return errors.New("could not initialize authentication")
	}
	app.passwordLogin = config.PasswordLogin
	app.developmentSignup = config.DevelopmentSignup
	app.release = config.SentryRelease
	if config.WhatsAppDatabaseURL != "" {
		app.whatsapp, err = openWhatsAppService(ctx, config.WhatsAppDatabaseURL)
		if err != nil {
			return err
		}
		defer app.whatsapp.close()
		if config.FCMProjectID != "" {
			sender, err := newFCMSender(ctx, config.FCMProjectID, config.FCMCredentialsFile)
			if err != nil {
				return err
			}
			stopPush := startProfilePush(ctx, app.whatsapp.db, sender)
			defer stopPush()
		}
		app.whatsapp.developmentSignup = config.DevelopmentSignup
		if err = app.whatsapp.resume(); err != nil {
			return errors.New("could not restore mapped WhatsApp accounts")
		}
	}
	server := &http.Server{Addr: config.Addr, Handler: app.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	listener, err := net.Listen("tcp", config.Addr)
	if err != nil {
		return fmt.Errorf("HTTP listener: %w", err)
	}
	go func() { serverErr <- server.Serve(listener) }()
	log.Printf("IPO API listening on http://%s", config.Addr)
	sentry.NewLogger(ctx).Info().Emit("IPO API started")
	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server: %w", err)
		}
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return errors.New("HTTP server shutdown failed")
		}
	}
	return nil
}

func (a *api) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("PUT /api/push-devices/{installationID}", a.registerPushDevice)
	mux.HandleFunc("POST /api/auth/whatsapp/start", a.startWhatsAppLogin)
	mux.HandleFunc("POST /api/auth/whatsapp/status", a.whatsAppLoginStatus)
	mux.HandleFunc("POST /api/auth/whatsapp/exchange", a.exchangeWhatsAppLogin)
	mux.HandleFunc("POST /api/auth/refresh", a.refreshSession)
	mux.HandleFunc("POST /api/auth/device-tokens", a.createDeviceToken)
	mux.HandleFunc("POST /api/auth/device-tokens/exchange", a.exchangeDeviceToken)
	mux.HandleFunc("GET /api/sessions", a.listSessions)
	mux.HandleFunc("DELETE /api/sessions/{id}", a.removeSession)
	mux.HandleFunc("POST /api/logout-all", a.logoutAll)
	mux.HandleFunc("POST /api/businesses/{businessID}/whatsapp-accounts/{id}/disconnect", a.protect(false, a.disconnectWhatsAppAccount))
	mux.HandleFunc("GET /api/me", a.me)
	mux.HandleFunc("GET /api/businesses/{businessID}/staff", a.protect(true, a.listStaff))
	mux.HandleFunc("POST /api/businesses/{businessID}/staff", a.protect(true, a.createStaff))
	mux.HandleFunc("GET /api/businesses/{businessID}/customers", a.protect(false, a.listCustomers))
	mux.HandleFunc("POST /api/businesses/{businessID}/customers", a.protect(false, a.createCustomer))
	mux.HandleFunc("POST /api/businesses/{businessID}/contacts", a.protect(false, a.importContacts))
	mux.HandleFunc("GET /api/businesses/{businessID}/contacts", a.protect(false, a.lookupContact))
	mux.HandleFunc("DELETE /api/businesses/{businessID}/contacts", a.protect(false, a.removeContactName))
	mux.HandleFunc("GET /api/businesses/{businessID}/ipos", a.protect(false, a.listIPOs))
	mux.HandleFunc("POST /api/businesses/{businessID}/ipos", a.protect(true, a.createIPO))
	mux.HandleFunc("GET /api/businesses/{businessID}/form-types", a.protect(false, a.listFormTypes))
	mux.HandleFunc("GET /api/businesses/{businessID}/whatsapp-accounts", a.protect(false, a.listWhatsAppAccounts))
	mux.HandleFunc("POST /api/businesses/{businessID}/whatsapp-accounts", a.protect(false, a.createWhatsAppAccount))
	mux.HandleFunc("GET /api/businesses/{businessID}/whatsapp-accounts/{id}", a.protect(false, a.getWhatsAppAccount))
	mux.HandleFunc("POST /api/businesses/{businessID}/whatsapp-accounts/{id}/pair", a.protect(false, a.pairWhatsAppAccount))
	mux.HandleFunc("POST /api/businesses/{businessID}/whatsapp-accounts/{id}/connect", a.protect(false, a.connectWhatsAppAccount))
	mux.HandleFunc("POST /api/businesses/{businessID}/whatsapp-accounts/{id}/check-number", a.protect(false, a.checkWhatsAppNumber))
	mux.HandleFunc("POST /api/businesses/{businessID}/whatsapp-accounts/{id}/contact-profile", a.protect(false, a.whatsAppContactProfile))
	mux.HandleFunc("GET /api/businesses/{businessID}/whatsapp-accounts/{id}/connection", a.protect(false, a.whatsAppConnection))
	mux.HandleFunc("GET /api/businesses/{businessID}/whatsapp-accounts/{id}/qr", a.protect(false, a.whatsAppQR))
	mux.HandleFunc("GET /api/businesses/{businessID}/transactions", a.protect(false, a.listTransactions))
	mux.HandleFunc("POST /api/businesses/{businessID}/transactions", a.protect(false, a.createTransaction))
	mux.HandleFunc("GET /api/businesses/{businessID}/transactions/{id}", a.protect(false, a.getTransaction))
	mux.HandleFunc("PUT /api/businesses/{businessID}/transactions/{id}", a.protect(false, a.editTransaction))
	mux.HandleFunc("POST /api/businesses/{businessID}/transactions/{id}/cancel", a.protect(false, a.cancelTransaction))
	mux.HandleFunc("GET /api/businesses/{businessID}/transactions/{id}/history", a.protect(false, a.transactionHistory))
	mux.HandleFunc("GET /api/businesses/{businessID}/transactions/{id}/confirmation", a.protect(false, a.getConfirmation))
	handler := sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *api) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	var ready int
	if a.db.QueryRowContext(ctx, "SELECT 1").Scan(&ready) != nil {
		fail(w, 503, "database_unavailable", "Database is unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, 400, "invalid_json", "Supply one JSON object with supported fields (maximum 64 KiB)")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		fail(w, 400, "invalid_json", "Supply exactly one JSON object")
		return false
	}
	return true
}

func dbFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Record not found")
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			fail(w, 409, "conflict", "A record with this identity already exists")
			return
		case "23503", "23514", "23502", "22003", "22007", "22P02", "P0001":
			fail(w, 400, "invalid_record", "The record violates validation or references an unavailable record")
			return
		}
		log.Printf("database request failed (SQLSTATE %s)", pgErr.Code)
	} else {
		log.Printf("database request failed (%T)", err)
	}
	sentry.CaptureException(errors.New("Unexpected database failure"))
	sentry.NewLogger(context.Background()).Error().Emit("Unexpected database failure")
	fail(w, 500, "internal_error", "The request could not be completed")
}

func queryJSON(w http.ResponseWriter, r *http.Request, db *sql.DB, status int, query string, args ...any) {
	var data json.RawMessage
	if err := db.QueryRowContext(r.Context(), query, args...).Scan(&data); err != nil {
		dbFailure(w, err)
		return
	}
	writeJSON(w, status, data)
}
