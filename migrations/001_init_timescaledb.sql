CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE IF NOT EXISTS symbols (
    id UUID PRIMARY KEY,
    ticker TEXT NOT NULL UNIQUE,
    name TEXT,
    exchange TEXT,
    asset_class TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS prices (
    id UUID PRIMARY KEY,
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    ts TIMESTAMPTZ NOT NULL,
    open NUMERIC,
    high NUMERIC,
    low NUMERIC,
    close NUMERIC,
    volume NUMERIC
);

SELECT create_hypertable('prices', 'ts', if_not_exists => TRUE);

CREATE TABLE IF NOT EXISTS indicators (
    id UUID PRIMARY KEY,
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    ts TIMESTAMPTZ NOT NULL,
    indicator_type TEXT NOT NULL,
    value NUMERIC,
    metadata JSONB
);

CREATE TABLE IF NOT EXISTS predictions (
    id UUID PRIMARY KEY,
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    ts TIMESTAMPTZ NOT NULL,
    correction_probability NUMERIC,
    confidence NUMERIC,
    expected_duration TEXT,
    expected_strength NUMERIC,
    model_version TEXT
);

CREATE TABLE IF NOT EXISTS portfolios (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    cash NUMERIC,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS positions (
    id UUID PRIMARY KEY,
    portfolio_id UUID NOT NULL REFERENCES portfolios(id),
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    quantity NUMERIC,
    avg_cost NUMERIC,
    current_price NUMERIC,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY,
    portfolio_id UUID NOT NULL REFERENCES portfolios(id),
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    type TEXT NOT NULL,
    quantity NUMERIC,
    price NUMERIC,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS recommendations (
    id UUID PRIMARY KEY,
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    action TEXT NOT NULL,
    score NUMERIC,
    confidence NUMERIC,
    technical_score NUMERIC,
    ml_score NUMERIC,
    portfolio_score NUMERIC,
    risk_score NUMERIC,
    explanation JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    model_version TEXT
);
