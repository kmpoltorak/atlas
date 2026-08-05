package persistence

import (
	"errors"
	"fmt"

	"atlas/internal/domain/market"
)

// TimescaleDB exposes the first valid persistence connection contract for the approved model.
type TimescaleDB struct {
	Host string
	Port string
	DB   string
	User string
	Pass string

	bars map[string][]market.Bar
}

func NewTimescaleDB(host, port, db, user, pass string) *TimescaleDB {
	return &TimescaleDB{
		Host: host,
		Port: port,
		DB:   db,
		User: user,
		Pass: pass,
		bars: map[string][]market.Bar{},
	}
}

// DSN returns a minimal PostgreSQL-style DSN string for the connection boundary.
func (t *TimescaleDB) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", t.Host, t.Port, t.User, t.Pass, t.DB)
}

// SaveBars stores a symbol’s normalized bars in memory for the approved first persistence milestone.
func (t *TimescaleDB) SaveBars(symbol string, bars []market.Bar) error {
	if symbol == "" {
		return errors.New("symbol is required")
	}
	if bars == nil {
		return errors.New("bars are required")
	}
	if t.bars == nil {
		t.bars = map[string][]market.Bar{}
	}
	copied := make([]market.Bar, len(bars))
	copy(copied, bars)
	t.bars[symbol] = copied
	return nil
}

// GetBars returns the stored bars for the requested symbol when the in-memory persistence is used.
func (t *TimescaleDB) GetBars(symbol string) ([]market.Bar, error) {
	if symbol == "" {
		return nil, errors.New("symbol is required")
	}
	stored, ok := t.bars[symbol]
	if !ok {
		return nil, nil
	}
	copied := make([]market.Bar, len(stored))
	copy(copied, stored)
	return copied, nil
}
