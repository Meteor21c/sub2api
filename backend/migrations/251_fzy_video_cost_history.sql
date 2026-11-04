-- Historical prices remain accounting records. Store verified output duration
-- separately from requested duration, without prompts or media references.
ALTER TABLE fzy_video_billing_jobs
    ADD COLUMN actual_duration_seconds NUMERIC(12, 4)
    CHECK (actual_duration_seconds > 0 AND actual_duration_seconds <= 86400);

CREATE INDEX idx_fzy_video_personal_cost_history
    ON fzy_video_billing_jobs (user_id, model, created_at DESC)
    WHERE status = 'settled';
