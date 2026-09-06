package persistence

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"atlas/internal/domain/market"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// TimescaleDB is a real sqlx-backed persistence adapter for the collector.Storage contract,
// upserting bars idempotently against the symbols/prices schema (see migrations/).
type TimescaleDB struct {
	db *sqlx.DB
	// baseCtx is the request's context when this handle was scoped with WithContext, nil
	// otherwise. It is the cancellation signal, not a value bag — see dbContext.
	baseCtx context.Context
}

// WithContext returns a handle whose queries are cancelled when ctx is — used by HTTP handlers so
// a client that disconnects, or a request that times out, stops the database work it started
// instead of leaving it to run to completion against a connection nobody is waiting on.
//
// A shallow copy sharing the same *sqlx.DB, so it costs nothing and the pool stays one pool. This
// shape rather than a ctx parameter on all forty repository methods: the signature churn would
// have been the entire diff, and every call site would have had to be touched to pass a context
// most of them do not have.
func (t *TimescaleDB) WithContext(ctx context.Context) *TimescaleDB {
	if t == nil {
		return nil
	}
	scoped := *t
	scoped.baseCtx = ctx
	return &scoped
}

// DSN builds a PostgreSQL connection URL from discrete config values. Built with net/url rather
// than fmt so a password containing a space, quote or backslash escapes correctly instead of
// silently truncating the key/value DSN it used to produce.
func DSN(host, port, dbName, user, pass, sslMode string) string {
	if sslMode == "" {
		sslMode = "disable"
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, pass),
		Host:     net.JoinHostPort(host, port),
		Path:     "/" + dbName,
		RawQuery: url.Values{"sslmode": {sslMode}}.Encode(),
	}
	return u.String()
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
	ctx, cancel := dbContext()
	defer cancel()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	symbolID, err := upsertSymbol(ctx, tx, symbol)
	if err != nil {
		return err
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
	ctx, cancel := dbContext()
	defer cancel()

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

// queryTimeout bounds every repository call that has no caller-supplied context. It is not a
// tuning knob for slow queries — an Atlas query that needs longer than this against its own
// TimescaleDB is a bug or a stuck connection, and letting it run forever is what turned a
// database hiccup into an HTTP handler that never returned.
const queryTimeout = 10 * time.Second

// dbContext returns a context bounded by queryTimeout, derived from the request's context when
// this handle was scoped with WithContext. Callers must defer the cancel.
//
// The timeout is a backstop, not the mechanism: it bounds a query nobody cancelled. Cancellation
// is what makes an abandoned request stop costing anything, and it only exists on a scoped handle.
func (t *TimescaleDB) context() (context.Context, context.CancelFunc) {
	base := context.Background()
	if t != nil && t.baseCtx != nil {
		base = t.baseCtx
	}
	return context.WithTimeout(base, queryTimeout)
}

// dbContext is the unscoped form, for the background paths (the alert loop, migrations, startup)
// that have no request behind them.
func dbContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), queryTimeout)
}
