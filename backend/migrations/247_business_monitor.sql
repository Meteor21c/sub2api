-- Lightweight owner-only business monitoring. Independent tables keep rollback safe.
CREATE TABLE IF NOT EXISTS business_monitor_samples (
    id BIGSERIAL PRIMARY KEY,
    wallet_key TEXT NOT NULL,
    base_url TEXT NOT NULL,
    name TEXT NOT NULL,
    balance NUMERIC(24,8),
    status TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS business_monitor_samples_wallet_time ON business_monitor_samples(wallet_key, observed_at DESC);
CREATE INDEX IF NOT EXISTS business_monitor_samples_time ON business_monitor_samples(observed_at);

CREATE TABLE IF NOT EXISTS business_monitor_credits (
    id BIGSERIAL PRIMARY KEY,
    sample_id BIGINT NOT NULL UNIQUE REFERENCES business_monitor_samples(id),
    wallet_key TEXT NOT NULL,
    amount NUMERIC(24,8) NOT NULL,
    observed_increase NUMERIC(24,8) NOT NULL,
    kind TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS business_monitor_credits_wallet_time ON business_monitor_credits(wallet_key, observed_at);

CREATE TABLE IF NOT EXISTS business_monitor_credit_overrides (
    wallet_key TEXT NOT NULL,
    day DATE NOT NULL,
    amount NUMERIC(24,8) NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(wallet_key, day)
);

CREATE TABLE IF NOT EXISTS business_monitor_rate_state (
    state_key TEXT PRIMARY KEY,
    account_id BIGINT NOT NULL,
    account_name TEXT NOT NULL,
    kind TEXT NOT NULL,
    rate NUMERIC(24,8) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS business_monitor_rate_changes (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    account_name TEXT NOT NULL,
    kind TEXT NOT NULL,
    old_rate NUMERIC(24,8) NOT NULL,
    new_rate NUMERIC(24,8) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS business_monitor_rate_changes_time ON business_monitor_rate_changes(observed_at DESC);

CREATE TABLE IF NOT EXISTS business_monitor_daily (
    day DATE NOT NULL,
    wallet_key TEXT NOT NULL,
    payload JSONB NOT NULL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(day, wallet_key)
);
CREATE TABLE IF NOT EXISTS business_monitor_state (
    state_key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
