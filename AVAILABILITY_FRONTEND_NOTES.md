# Availability V2 frontend handoff

## Scope

- Target branch: `codex/availability-v2-frontend`
- Baseline: `5d51f4df969baf3c7cebfd5aec51bf4cab1a3aff`
- Only `frontend/**` and this root note were changed.
- No production server, real upstream, deployment, push, or production build was used.

## Delivered

- Replaced the legacy `/admin/channel-test` view with the Availability V2 workspace:
  group-first selection, server-scoped account cards, independently scrollable account/model rails, continuous conversation, live route events, and a permanent server-backed history section.
- Account cards render the server-provided rank, priority, load factor (used as the weight), concurrency/queue data when available, eligibility, and server reason. The UI preserves server ordering/rank semantics and never converts load factor into a probability.
- Priority and load factor edits use the existing `adminAPI.accounts.update` endpoint with only the changed field. Drafts roll back on validation/network errors; successful updates reload the V2 catalog so the server scheduler can reorder the rail.
- Automatic routing omits `account_id` when creating a conversation. Pinning an account sends `account_id` and keeps that scope; the UI does not silently substitute another account. Catalog responses are the only source for group membership.
- Model cards distinguish upstream declaration, downstream policy, effective mapping/passthrough, source, eligible account count, and the latest real test. Latest test timing includes visible relative and absolute timestamps, first-response latency, total latency, actual account, and actual model.
- Conversations and turns are loaded and searched through `/api/v1/admin/channel-test/conversations`; reset/new conversation only clears the local active view and never deletes server history. Conversation detail supports paged turn loading and preserves all attempts.
- Authenticated POST SSE is consumed through `fetch`, not `EventSource`. The UI renders ordered attempts and routing events, actual account/model/endpoint, reason/error, and terminal status. Abort cancels the current stream; a missing terminal event or stream network failure refreshes persisted conversation state and never auto-retries the turn.
- `client_request_id` is generated once per submitted turn and passed unchanged to the server. A pending-submit guard prevents double submission while conversation creation is in flight.
- Legacy account/group debug-test API functions remain in `frontend/src/api/admin/accounts.ts`; the new view does not remove or rewrite that compatibility surface.

## Verification

From `frontend/`:

- `pnpm run lint:check` — passed
- `pnpm run typecheck` — passed
- `pnpm run check:i18n` — passed
- `pnpm run test:run` — passed, 270 files / 1998 tests
- `git diff --check` — passed

Focused coverage is in `src/views/admin/__tests__/ChannelTestView.spec.ts` and `src/api/__tests__/admin.availability.spec.ts`, including group scoping, automatic vs pinned account payloads, attempt rendering, save rollback, SSE headers/terminal parsing, server history pagination/search, and stream-disconnect recovery.

## Contract notes

- The frontend follows the fixed V2 paths and JSON/SSE shapes in `AVAILABILITY_V2_HANDOFF.md`.
- The contract has no dedicated cancel endpoint; cancellation uses `AbortController`, which the backend persists as cancelled. A disconnected stream can reconnect through the same POST endpoint with the original `client_request_id`, allowing the backend's idempotent replay/reattach path to finish the original turn without creating a second turn; status refresh remains available as a fallback.
- The frontend was verified with fixtures and static checks only; backend integration and real-upstream behavior remain for the cross-worktree acceptance pass.
