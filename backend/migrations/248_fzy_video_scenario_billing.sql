-- Preserve existing token reservations; new FZY Kling requests freeze their
-- per-second scenario tariff and requested duration in the same transaction.
ALTER TABLE fzy_video_billing_jobs
    ADD COLUMN IF NOT EXISTS billing_mode TEXT NOT NULL DEFAULT 'token',
    ADD COLUMN IF NOT EXISTS official_per_second NUMERIC(20, 8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS sale_per_second NUMERIC(20, 8) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS duration_seconds INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS resolution TEXT NOT NULL DEFAULT '';

ALTER TABLE fzy_video_billing_jobs
    DROP CONSTRAINT IF EXISTS fzy_video_billing_jobs_official_per_million_check,
    DROP CONSTRAINT IF EXISTS fzy_video_billing_jobs_sale_per_million_check;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'fzy_video_billing_tariff_units_check'
                     AND conrelid = 'fzy_video_billing_jobs'::regclass) THEN
        ALTER TABLE fzy_video_billing_jobs
            ADD CONSTRAINT fzy_video_billing_tariff_units_check CHECK (
                (billing_mode = 'token' AND official_per_million > 0 AND sale_per_million > 0)
                OR (billing_mode = 'second' AND official_per_second > 0
                    AND sale_per_second > 0 AND duration_seconds BETWEEN 3 AND 30)
            );
    END IF;
END $$;
