-- Preserve all creation-time tariffs and input metrics across asynchronous
-- polling, reconciliation and process restarts. Existing reservations keep
-- using their original typed price columns when the snapshot is empty.
ALTER TABLE fzy_video_billing_jobs
    ADD COLUMN IF NOT EXISTS price_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS estimated_surcharge_amount NUMERIC(20, 8) NOT NULL DEFAULT 0;
