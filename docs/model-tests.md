# Model tests

The authenticated `/model-tests` page shows scheduled group benchmarks and the
current user's own tests. `/admin/model-tests` configures the schedules. New
installations have no plans and make no inference calls until an administrator
enables a plan or a user submits a test.

## Behavior

- The only prompts are the fixed pelican SVG and candy puzzle presets.
- Each enabled plan selects a display label, group, model and one preset. Add
  two plans to test both presets for the same group/model.
- Enabling a plan starts its first occurrence, then repeats every two hours.
  A delayed scheduler creates one occurrence, without replaying missed slots.
- Candy grading accepts a final `FINAL_ANSWER: 21` line. Other integers are
  incorrect; missing/malformed final answers remain ungraded. Pelican output is
  displayed with the existing sanitized SVG image renderer and is not scored.
- All test records expire 24 hours after submission. Reads enforce expiry even
  before the minute cleanup tick. The cleanup deletes only test tables; usage
  ledgers and normal account history retain their existing behavior.

## Billing and access

User submissions contain an owned key ID, model, preset and submission UUID.
They never contain a key credential or a custom prompt. The server rechecks key
ownership, activity and group binding before execution and reads its credential
only for the call. The original verified client IP is preserved for key ACLs.

User inference re-enters the fully registered application router at
`POST /v1/responses`, with no copied JWT or forwarded headers. Normal API-key
authentication, group/model permissions, quotas, subscription checks, scheduling
and usage billing apply. The worker does not mutate wallets or create its own
billing records. `GET /v1/models` discovers the selected key's visible models.

Scheduled probes use the existing administrator group tester. They consume
upstream capacity but do not debit a customer balance. Public DTOs contain no
account names, upstream endpoints, raw errors, credentials or client addresses.
Personal result queries require the original owner on both list and detail;
scheduled results are visible to authenticated users.

## Administrator result management

The schedule settings page includes the last 24 hours of public test records.
Its administrator-only APIs expose a snapshot of the final upstream account
and the account attempts (account ID/name, success and HTTP status only). Both
successful and failed group probes save this metadata. Older records without
snapshots remain visible with an explicit missing-channel label. Customer APIs
omit all routing snapshots and manual/retry metadata.

Administrators can delete one queued or completed public result. Deleting a
queued result prevents it from being claimed; running calls must finish first.
Deleted results disappear from customer history, summaries and detail requests.
Their tombstones retain submission idempotency until normal expiration, and
workers cannot write the deleted content back.

Retry creates a new public result using the original parameters and the normal
group scheduler. It preserves the previous result and may select another
upstream account. A plan can also run immediately while paused. Neither action
changes its next scheduled occurrence. Both require an idempotency UUID, share
the bounded persistent queue, and consume upstream capacity. These actions never
replay or debit another user's personal tests.

Administrator APIs (all under `/api/v1/admin/model-tests`):

- `GET /jobs` and `GET /jobs/:id`
- `DELETE /jobs/:id`
- `POST /jobs/:id/retry` with `client_request_id`
- `POST /plans/:id/run` with `client_request_id`

## Durable queue

The queue lives in PostgreSQL, with two concurrent inferences across app
instances, a maximum of 200 queued/running jobs, and per-user limits of two active
jobs and 20 submissions/hour. Submission UUIDs and scheduled occurrence keys
prevent duplicate enqueues. Claiming uses database locks and a 12-minute lease;
each inference has a 10-minute timeout. Output collection is capped at 1 MiB.

Browser connections do not own task contexts. Queued jobs can be claimed after
an app restart. An interrupted running job is never automatically replayed,
because its upstream charge may already exist. Known interruptions are saved;
abandoned leases become interrupted on the cleanup tick. Changing or deleting a
plan cancels its queued occurrences. A call already in progress may finish.

## Validation

Service tests cover browser cancellation, normal-router dispatch, key ownership
and revalidation, error redaction, SVG whitespace, candy grading and output
limits. PostgreSQL integration tests cover simultaneous idempotent submission,
queue concurrency, owner isolation, restart claims, leases, 24-hour expiry and
the two-hour scheduling cadence. Frontend tests cover key-ID-only submission,
history restoration, safe SVG previews and administrator plan configuration.
Production acceptance must not submit paid inference tasks or alter wallets.
