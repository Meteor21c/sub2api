-- Canvas pilot stores metadata only; media retention stays with existing services.
CREATE TABLE IF NOT EXISTS canvas_pilot_projects (
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id VARCHAR(100) NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    data JSONB NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    data_bytes INTEGER NOT NULL CHECK (data_bytes BETWEEN 1 AND 1048576),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, project_id)
);
CREATE INDEX IF NOT EXISTS canvas_pilot_projects_owner_updated
    ON canvas_pilot_projects(owner_id, updated_at DESC);
