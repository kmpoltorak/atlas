package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"atlas/internal/domain/market"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// TimescaleDB is a real sqlx-backed persistence adapter for the collector.Storage contract,
// upserting bars idempotently against the symbols/prices schema (see migrations/).
type TimescaleDB struct {
	db *sqlx.DB
}

// DSN builds a PostgreSQL connection string from discrete config values.
func DSN(host, port, dbName, user, pass string) string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, pass, dbName)
}

// NewTimescaleDB opens and verifies a connection. Failing fast here (rather than lazily on
// first query) matches the rest of the app's stance: Atlas can't give a truthful
// recommendation without real historical data, so a broken DB connection should be loud.
func NewTimescaleDB(ctx context.Context, dsn string) (*TimescaleDB, error) {
	db, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to timescaledb: %w", err)
	}
	return &TimescaleDB{db: db}, nil
}

func (t *TimescaleDB) Close() error {
	return t.db.Close()
}

func (t *TimescaleDB) Ping(ctx context.Context) error {
	return t.db.PingContext(ctx)
}

// SaveBars idempotently upserts a symbol's bars: the same (symbol, timestamp) pair overwrites
// the prior row instead of duplicating it, so re-collecting an overlapping window is safe.
func (t *TimescaleDB) SaveBars(symbol string, bars []market.Bar) error {
	if symbol == "" {
		return errors.New("symbol is required")
	}
	if bars == nil {
		return errors.New("bars are required")
	}
	ctx := context.Background()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	var symbolID string
	if err := tx.GetContext(ctx, &symbolID,
		`INSERT INTO symbols (ticker) VALUES ($1)
		 ON CONFLICT (ticker) DO UPDATE SET ticker = EXCLUDED.ticker
		 RETURNING id`, symbol); err != nil {
		return fmt.Errorf("upsert symbol %s: %w", symbol, err)
	}

	for _, bar := range bars {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO prices (symbol_id, ts, open, high, low, close, volume)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (symbol_id, ts) DO UPDATE SET
			   open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low,
			   close = EXCLUDED.close, volume = EXCLUDED.volume`,
			symbolID, bar.Timestamp, bar.Open, bar.High, bar.Low, bar.Close, bar.Volume); err != nil {
			return fmt.Errorf("upsert price bar for %s at %s: %w", symbol, bar.Timestamp, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// GetBars returns a symbol's previously stored bars, oldest first.
func (t *TimescaleDB) GetBars(symbol string) ([]market.Bar, error) {
	if symbol == "" {
		return nil, errors.New("symbol is required")
	}
	ctx := context.Background()

	var rows []struct {
		Ts     time.Time `db:"ts"`
		Open   float64   `db:"open"`
		High   float64   `db:"high"`
		Low    float64   `db:"low"`
		Close  float64   `db:"close"`
		Volume float64   `db:"volume"`
	}
	if err := t.db.SelectContext(ctx, &rows,
		`SELECT p.ts, p.open, p.high, p.low, p.close, p.volume
		 FROM prices p
		 JOIN symbols s ON s.id = p.symbol_id
		 WHERE s.ticker = $1
		 ORDER BY p.ts ASC`, symbol); err != nil {
		return nil, fmt.Errorf("select bars for %s: %w", symbol, err)
	}

	bars := make([]market.Bar, 0, len(rows))
	for _, row := range rows {
		bars = append(bars, market.Bar{
			Symbol: symbol, Open: row.Open, High: row.High, Low: row.Low,
			Close: row.Close, Volume: row.Volume, Timestamp: row.Ts,
		})
	}
	return bars, nil
}
