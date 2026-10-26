-- 239: durable administrator-scoped availability V2 conversations, turns,
-- attempts and replayable SSE events.
--
-- These tables intentionally keep owner_user_id on every record.  The
-- application repeats that predicate on every read/write, which prevents an
-- administrator from discovering another administrator's history by ID and
-- keeps the event stream independently auditable.

CREATE TABLE IF NOT EXISTS availability_conversations (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    account_id BIGINT,
    model TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT 'Availability test',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS availability_conversations_owner_updated_idx
    ON availability_conversations (owner_user_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS availability_turns (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    prompt TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    client_request_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled')),
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    first_response_ms BIGINT,
    total_ms BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT availability_turns_client_request_uq
        UNIQUE (conversation_id, client_request_id)
);

CREATE INDEX IF NOT EXISTS availability_turns_owner_conversation_idx
    ON availability_turns (owner_user_id, conversation_id, id ASC);

CREATE TABLE IF NOT EXISTS availability_attempts (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    turn_id BIGINT NOT NULL,
    attempt_index INTEGER NOT NULL,
    account_id BIGINT NOT NULL,
    account_name TEXT NOT NULL DEFAULT '',
    requested_model TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    endpoint TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    first_response_ms BIGINT,
    total_ms BIGINT,
    status_code INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS availability_attempts_owner_turn_idx
    ON availability_attempts (owner_user_id, conversation_id, turn_id, attempt_index ASC, id ASC);

CREATE INDEX IF NOT EXISTS availability_attempts_recent_test_idx
    ON availability_attempts (owner_user_id, account_id, model, completed_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS availability_events (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    turn_id BIGINT NOT NULL,
    seq BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    event_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    payload JSONB NOT NULL,
    CONSTRAINT availability_events_turn_seq_uq UNIQUE (turn_id, seq)
);

CREATE INDEX IF NOT EXISTS availability_events_owner_turn_seq_idx
    ON availability_events (owner_user_id, conversation_id, turn_id, seq ASC);
