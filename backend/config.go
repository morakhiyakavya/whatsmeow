package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type configuration struct {
	DatabaseURL, Addr, BusinessName, AdminName, AdminPhone, AdminPassword, WhatsAppDatabaseURL string
	PasswordLogin                                                                              bool
	DevelopmentSignup                                                                          bool
}

func loadConfig(path string) (configuration, error) {
	file, err := os.Open(path)
	if err != nil && !(errors.Is(err, os.ErrNotExist) && os.Getenv("DATABASE_URL") != "") {
		return configuration{}, errors.New("cannot open configuration file; create .env or run scripts/Setup-LocalDatabase.ps1")
	}
	values := map[string]string{}
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return configuration{}, errors.New("invalid .env line; expected KEY=value")
			}
			value = strings.TrimSpace(value)
			if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
				value = value[1 : len(value)-1]
			}
			values[strings.TrimSpace(key)] = value
		}
		if scanner.Err() != nil {
			return configuration{}, errors.New("could not read .env")
		}
	}
	get := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return values[key]
	}
	c := configuration{DatabaseURL: get("DATABASE_URL"), Addr: get("API_ADDR"), BusinessName: get("BOOTSTRAP_BUSINESS_NAME"), AdminName: get("BOOTSTRAP_ADMIN_NAME"), AdminPhone: get("BOOTSTRAP_ADMIN_PHONE"), AdminPassword: get("BOOTSTRAP_ADMIN_PASSWORD"), WhatsAppDatabaseURL: get("WHATSAPP_DATABASE_URL")}
	setting := get("ALLOW_PASSWORD_LOGIN")
	if setting != "" && setting != "true" && setting != "false" {
		return c, errors.New("ALLOW_PASSWORD_LOGIN must be true or false")
	}
	c.PasswordLogin = setting == "true"
	environment := get("APP_ENV")
	if environment != "" && environment != "production" && environment != "development" {
		return c, errors.New("APP_ENV must be development or production")
	}
	c.DevelopmentSignup = environment == "development"
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.Addr == "" {
		c.Addr = "127.0.0.1:8180"
		if port := get("PORT"); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return c, errors.New("PORT must be between 1 and 65535")
			}
			c.Addr = "0.0.0.0:" + port
		}
	}
	return c, nil
}

func bootstrapAdmin(ctx context.Context, db *sql.DB, c configuration) error {
	if !validText(c.BusinessName, 256) || !validText(c.AdminName, 256) || !phonePattern.MatchString(c.AdminPhone) || !validPassword(c.AdminPassword) {
		return errors.New("bootstrap requires business/admin names, an international phone, and a password of 12–72 bytes")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("could not start bootstrap transaction")
	}
	defer tx.Rollback()
	// A transaction-scoped lock also makes concurrent bootstrap invocations safe.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(73195620)"); err != nil {
		return errors.New("could not lock bootstrap")
	}
	var userID, businessID string
	err = tx.QueryRowContext(ctx, `SELECT u.id,b.id FROM ipo_pos.users u
        JOIN ipo_pos.business_memberships m ON m.user_id=u.id
        JOIN ipo_pos.businesses b ON b.id=m.business_id
        JOIN ipo_pos.user_credentials c ON c.user_id=u.id
        WHERE u.phone_number=$1 AND b.name=$2 AND m.role='admin'
        AND m.disabled_at IS NULL AND u.disabled_at IS NULL AND b.archived_at IS NULL`, c.AdminPhone, c.BusinessName).Scan(&userID, &businessID)
	if err == nil {
		fmt.Printf("Initial admin already exists: user=%s business=%s. Password preserved.\n", userID, businessID)
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return errors.New("could not check existing admin")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("could not hash bootstrap password")
	}
	err = tx.QueryRowContext(ctx, "INSERT INTO ipo_pos.users(name,phone_number) VALUES($1,$2) RETURNING id", c.AdminName, c.AdminPhone).Scan(&userID)
	if err != nil {
		return errors.New("could not create initial admin; phone may already belong to another user")
	}
	err = tx.QueryRowContext(ctx, "INSERT INTO ipo_pos.businesses(name) VALUES($1) RETURNING id", c.BusinessName).Scan(&businessID)
	if err != nil {
		return errors.New("could not create initial business")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO ipo_pos.business_memberships(business_id,user_id,role) VALUES($1,$2,'admin')", businessID, userID); err != nil {
		return errors.New("could not create admin membership")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO ipo_pos.user_credentials(user_id,password_hash) VALUES($1,$2)", userID, string(hash)); err != nil {
		return errors.New("could not save admin credentials")
	}
	if err = tx.Commit(); err != nil {
		return errors.New("could not commit bootstrap")
	}
	fmt.Printf("Created initial admin: user=%s business=%s. Sign in with the BOOTSTRAP_ADMIN_PHONE/PASSWORD in .env.\n", userID, businessID)
	return nil
}
