-- Model benchmarks have their own short-lived records. Billing/usage ledgers
-- remain governed by their existing retention policy.
CREATE TABLE IF NOT EXISTS model_test_plans (
    id BIGSERIAL PRIMARY KEY,
    label VARCHAR(80) NOT NULL,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model VARCHAR(200) NOT NULL,
    test_kind VARCHAR(16) NOT NULL CHECK (test_kind IN ('pelican', 'candy')),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    next_run_at TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS model_test_plans_due ON model_test_plans(next_run_at)
    WHERE enabled AND deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS model_test_jobs (
    id UUID PRIMARY KEY,
    source VARCHAR(16) NOT NULL CHECK (source IN ('scheduled', 'user')),
    owner_user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    plan_id BIGINT REFERENCES model_test_plans(id) ON DELETE SET NULL,
    group_id BIGINT NOT NULL,
    label VARCHAR(80) NOT NULL,
    model VARCHAR(200) NOT NULL,
    test_kind VARCHAR(16) NOT NULL CHECK (test_kind IN ('pelican', 'candy')),
    status VARCHAR(16) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'interrupted')),
    verdict VARCHAR(16) NOT NULL DEFAULT 'ungraded'
        CHECK (verdict IN ('correct', 'incorrect', 'ungraded')),
    answer BIGINT,
    content TEXT NOT NULL DEFAULT '' CHECK (octet_length(content) <= 1048576),
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    total_ms BIGINT NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    client_request_id UUID,
    client_ip VARCHAR(64) NOT NULL DEFAULT '',
    scheduled_for TIMESTAMPTZ,
    worker_id UUID,
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours',
    CHECK ((source = 'user' AND owner_user_id IS NOT NULL) OR
           (source = 'scheduled' AND owner_user_id IS NULL)),
    UNIQUE (owner_user_id, client_request_id),
    UNIQUE (plan_id, scheduled_for)
);
CREATE INDEX IF NOT EXISTS model_test_jobs_queue ON model_test_jobs(created_at)
    WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS model_test_jobs_owner ON model_test_jobs(owner_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS model_test_jobs_plan ON model_test_jobs(plan_id, created_at DESC);
CREATE INDEX IF NOT EXISTS model_test_jobs_expiry ON model_test_jobs(expires_at);
