package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type qDB struct {
	db      *sql.DB
	dialect string
}

func isPostgresDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

func sqlstoreDialect(d *qDB) string {
	if d.dialect == "postgres" {
		return "postgres"
	}
	return "sqlite3"
}

func openDatabase(target string) (*qDB, error) {
	if isPostgresDSN(target) {
		db, err := sql.Open("pgx", target)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(5)
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("postgres connection: %w", err)
		}
		return &qDB{db: db, dialect: "postgres"}, nil
	}
	dsn := "file:" + strings.TrimPrefix(target, "file:") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &qDB{db: db, dialect: "sqlite"}, nil
}

func (d *qDB) rebind(query string) string {
	if d.dialect != "postgres" {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (d *qDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.db.ExecContext(ctx, d.rebind(query), args...)
}

func (d *qDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, d.rebind(query), args...)
}

func (d *qDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, d.rebind(query), args...)
}

func (d *qDB) Close() error { return d.db.Close() }
