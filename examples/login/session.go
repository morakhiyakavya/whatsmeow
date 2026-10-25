package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
)

func sessionConfig(dsn string) (*pgx.ConnConfig, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("set WHATSAPP_DATABASE_URL to your PostgreSQL connection URL; SQLite sessions are not used")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		// Driver parse errors can include the original URL and password.
		return nil, errors.New("invalid WHATSAPP_DATABASE_URL; use a PostgreSQL URL or keyword connection string")
	}
	config.RuntimeParams["search_path"] = "whatsapp"
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 10 * time.Second
	}
	return config, nil
}

func openSessionStore(ctx context.Context, dsn string) (*sqlstore.Container, error) {
	config, err := sessionConfig(dsn)
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	container := sqlstore.NewWithDB(db, "pgx", nil)
	setupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = db.PingContext(setupCtx); err == nil {
		_, err = db.ExecContext(setupCtx, "CREATE SCHEMA IF NOT EXISTS whatsapp")
	}
	if err == nil {
		err = container.Upgrade(setupCtx)
	}
	if err != nil {
		_ = container.Close()
		if setupCtx.Err() != nil {
			return nil, fmt.Errorf("initialize PostgreSQL session storage: %w", setupCtx.Err())
		}
		// Report an actionable SQLSTATE without logging connection credentials.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return nil, fmt.Errorf("initialize PostgreSQL session storage failed (SQLSTATE %s); check database credentials and schema permissions", pgErr.Code)
		}
		return nil, errors.New("initialize PostgreSQL session storage failed; check the connection URL, server availability, credentials, and TLS settings")
	}
	return container, nil
}

func selectSession(ctx context.Context, container *sqlstore.Container, fresh bool, sessionID string) (*store.Device, error) {
	if fresh && sessionID != "" {
		return nil, errors.New("use either -new-session or -session, not both")
	}
	if fresh {
		return container.NewDevice(), nil
	}
	if sessionID != "" {
		jid, err := types.ParseJID(sessionID)
		if err != nil || jid.IsEmpty() || (jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer) {
			return nil, errors.New("invalid session ID; use the device JID printed after connection")
		}
		device, err := container.GetDevice(ctx, jid)
		if err != nil {
			return nil, err
		}
		if device == nil {
			return nil, errors.New("selected session does not exist; check -session or use -new-session to pair")
		}
		return device, nil
	}
	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		return nil, err
	}
	switch len(devices) {
	case 0:
		return container.NewDevice(), nil
	case 1:
		return devices[0], nil
	default:
		return nil, errors.New("multiple saved accounts: use -session <device-JID> or -new-session; no account was selected")
	}
}
