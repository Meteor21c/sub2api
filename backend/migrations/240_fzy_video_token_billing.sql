-- Durable reservations for FZY asynchronous video billing. A reservation is
-- created before contacting the provider, then bound to its provider task ID.
-- Balance is held in users.frozen_balance until settlement or release.
CREATE TABLE IF NOT EXISTS fzy_video_billing_jobs (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    account_id BIGINT NOT NULL,
    task_id TEXT,
    model TEXT NOT NULL,
    scene_code TEXT NOT NULL,
    scene_name TEXT NOT NULL,
    currency TEXT NOT NULL CHECK (currency IN ('CNY', 'USD')),
    official_per_million NUMERIC(20, 8) NOT NULL CHECK (official_per_million > 0),
    discount_rate NUMERIC(20, 8) NOT NULL CHECK (discount_rate > 0),
    markup_rate NUMERIC(20, 8) NOT NULL CHECK (markup_rate > 0),
    sale_per_million NUMERIC(20, 8) NOT NULL CHECK (sale_per_million > 0),
    hold_amount NUMERIC(20, 8) NOT NULL CHECK (hold_amount >= 0),
    actual_amount NUMERIC(20, 8),
    output_tokens BIGINT,
    status TEXT NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved', 'pending', 'settled', 'released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_checked_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    CONSTRAINT fzy_video_billing_task_unique UNIQUE (account_id, task_id)
);

CREATE INDEX IF NOT EXISTS fzy_video_billing_pending_idx
    ON fzy_video_billing_jobs (last_checked_at, created_at, id)
    WHERE status IN ('reserved', 'pending');

CREATE INDEX IF NOT EXISTS fzy_video_billing_user_task_idx
    ON fzy_video_billing_jobs (user_id, api_key_id, task_id);
