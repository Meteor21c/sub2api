-- Routing snapshots are private administrator metadata; the customer DTO omits
-- them. Manual probes share scheduled results and the same 24-hour lifetime.
ALTER TABLE model_test_jobs
    ADD COLUMN IF NOT EXISTS upstream_account_id BIGINT,
    ADD COLUMN IF NOT EXISTS upstream_account_name VARCHAR(256) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS upstream_attempts JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS manual BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS retry_of UUID,
    ADD COLUMN IF NOT EXISTS admin_actor_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS admin_request_id UUID,
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE UNIQUE INDEX IF NOT EXISTS model_test_jobs_admin_request
    ON model_test_jobs(admin_actor_id, admin_request_id)
    WHERE admin_request_id IS NOT NULL;
