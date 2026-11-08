package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestWorkerNotificationsIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable schema_test database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var user string
	if err = db.QueryRow("SELECT current_user").Scan(&user); err != nil || user != "schema_test" {
		t.Fatal("requires disposable schema_test database")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &whatsAppService{ctx: ctx, accounts: map[string]*accountConnection{
		"business/account": {deliveries: make(chan struct{}, 1)},
		"other/account":    {deliveries: make(chan struct{}, 1)},
	}, pushWake: make(chan struct{}, 1)}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["application_name"] = "confirmation-notification-fixture"
	s.wg.Add(1)
	go s.listenForWork(config)
	defer func() { cancel(); s.wg.Wait() }()
	await := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("committed work did not wake the listener")
		}
	}
	await(s.pushWake)
	for _, c := range s.accounts {
		await(c.deliveries)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("SELECT pg_notify('ipo_worker_wake','business/account')"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.accounts["business/account"].deliveries:
		t.Fatal("uncommitted work woke the worker")
	case <-time.After(50 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	await(s.accounts["business/account"].deliveries)
	select {
	case <-s.accounts["other/account"].deliveries:
		t.Fatal("notification woke another account")
	default:
	}
	if _, err = db.Exec("SELECT pg_notify('ipo_worker_wake','push')"); err != nil {
		t.Fatal(err)
	}
	await(s.pushWake)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("SELECT pg_notify('ipo_worker_wake','business/account')"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.accounts["business/account"].deliveries:
		t.Fatal("rolled-back work woke the worker")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err = db.Exec("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name='confirmation-notification-fixture'"); err != nil {
		t.Fatal(err)
	}
	await(s.pushWake)
	for _, c := range s.accounts {
		await(c.deliveries)
	}
}
