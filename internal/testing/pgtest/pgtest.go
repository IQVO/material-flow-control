//go:build integration

// Package pgtest boots ONE Postgres container per test binary (testcontainers) and gives every
// test its own database cloned from a migrated template. It never reads a URL from the
// environment and never skips: no Docker is a failure (TestPostgresIntegrationTestsUseTestcontainers).
package pgtest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/postgres"
)

const (
	user     = "mfc_test"
	password = "mfc_test"
	template = "mfc_template"
)

var (
	once      sync.Once
	container *tcpostgres.PostgresContainer
	errStart  error
	seq       atomic.Int64
)

func start() {
	ctx := context.Background()
	container, errStart = tcpostgres.Run(ctx, "postgres:16-alpine", tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername(user), tcpostgres.WithPassword(password),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if errStart != nil {
		return
	}
	if errStart = admin(ctx, `CREATE DATABASE `+template); errStart != nil {
		return
	}
	url, err := dbURL(ctx, template)
	if err != nil {
		errStart = err
		return
	}
	for range 2 { // boot runs the migrations on every start; the second run must be a no-op
		if errStart = postgres.RunMigrations(url); errStart != nil {
			return
		}
	}
}

func dbURL(ctx context.Context, db string) (string, error) {
	base, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", err
	}
	cfg, err := pgx.ParseConfig(base)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, password, cfg.Host, cfg.Port, db), nil
}

func admin(ctx context.Context, sql string) error {
	url, err := dbURL(ctx, "postgres")
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

// URL is the DSN of a fresh migrated database.
func URL(t *testing.T) string {
	t.Helper()
	once.Do(start)
	if errStart != nil {
		t.Fatalf("start postgres: %v", errStart)
	}
	ctx := context.Background()
	name := fmt.Sprintf("t_%d", seq.Add(1))
	if err := admin(ctx, `CREATE DATABASE `+name+` TEMPLATE `+template); err != nil {
		t.Fatalf("create database: %v", err)
	}
	url, err := dbURL(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// Pool opens a pool on a fresh database, closed at cleanup.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// BarePool opens a pool on a fresh database WITHOUT NewPool's session settings (AfterConnect),
// closed at cleanup: what a transaction pooler (PgBouncer pool_mode=transaction) leaves a
// transaction with. carry (Task 13 review): the pattern of amr-fleet-control's pgtest.BarePool.
func BarePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
