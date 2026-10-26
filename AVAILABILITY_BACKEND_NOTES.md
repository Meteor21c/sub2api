# Availability V2 backend notes

## Implemented

- Added migration `backend/migrations/239_availability_v2.sql` for durable,
  administrator-scoped conversations, turns, attempts, and ordered SSE
  events. Every record carries `owner_user_id`; reads and writes repeat the
  owner and conversation/turn relationship checks.
- Added the fixed admin API under `/api/v1/admin/channel-test`:
  `GET /catalog`, conversation list/create/detail, turn SSE, and idempotent
  turn cancellation. The existing admin authentication middleware supplies
  the owner identity; handlers never accept an owner ID from the client.
- Added raw-SQL repository methods, service wiring, handler wiring, generated
  server wiring, and routes. Conversation history is paginated and searchable
  and has no deletion or expiry path.
- `client_request_id` is unique per conversation. Repeating the same request
  replays the existing turn; reusing it with a different prompt returns a
  conflict. Turn context is constructed only from earlier `succeeded` turns.
- V2 uses the existing `AccountTestService.TestAccountDebug` and
  `TestGroupDebug` paths. Automatic group mode keeps the real scheduler and
  failover behavior; selected-account mode invokes only that account. The
  observer records concrete account/model/endpoint attempt events before the
  upstream call, redacts endpoint credentials, persists content deltas and
  terminal snapshots, and preserves the legacy debug-test endpoint.
- SSE events receive durable per-turn sequence numbers and are replayed from
  the database for reconnects and completed turns. Client disconnect and
  cancellation persist `cancelled`; cancellation is safe to repeat. Stale
  running attempts are closed before a recovered worker continues the turn.
- Catalog data comes from real group membership, account mappings, persisted
  upstream model metadata snapshots, the group allowlist, and available
  concurrency-load data. Recent tests are owner-scoped. CN accounts without a
  snapshot or explicit mapping do not receive fabricated Claude/OpenAI model
  entries. Existing account update APIs remain the configuration/edit path;
  no duplicate priority or load-factor endpoint was added.

## Verification

Run locally from `backend/`:

```text
go test ./internal/service -run 'Test(Availability|BuildAvailability|AccountTest)' -count=1
go test -race ./internal/service -run 'Test(Availability|BuildAvailability|AccountTest)' -count=1
go test ./internal/handler/admin ./internal/server/routes ./internal/repository ./cmd/server -run '^$'
go test ./migrations -run '^Test.*Migration' -count=1
git diff --check
```

All commands passed. The Wire generator was attempted, but it produced no
output for an extended period in this worktree; the generated server wiring
was updated explicitly and the server package compile test passed.

## Remaining limits

- No PostgreSQL integration test was run here, so migration execution and raw
  repository SQL still need the normal local/staging database validation.
- The in-memory `runs` map and SSE fan-out are process-local. The database
  uniqueness constraint prevents duplicate turn rows, but there is not yet a
  distributed worker claim/lease for two backend instances that concurrently
  recover the same `running` turn. Multi-instance deployment should add that
  claim protocol before treating cross-instance retry deduplication as
  complete.
- Per the handoff constraints, no production server, real upstream, push, or
  deployment was accessed.
