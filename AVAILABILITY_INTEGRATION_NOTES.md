# Availability V2 integration notes

## Integrated scope

- Integration branch: `codex/availability-v2-integration`
- Baseline: `5d51f4df969baf3c7cebfd5aec51bf4cab1a3aff`
- Backend source commit: `61affee6a343e9cdaa2bc608447966d29957904`
- Frontend source commit: `e0143214f8d33562c7a97c17856103409bc7da`
- QA source commit: `ac345c972`

The backend, frontend, and QA changes were combined without conflicts. The
frontend and backend contracts agree on the `/api/v1/admin/channel-test`
catalog, conversation, detail, and authenticated POST SSE shapes. The legacy
debug-test routes remain registered.

The QA source-evidence gate was corrected to verify the stream route and the
wire payload evidence across the backend source set. Its previous check
incorrectly required the route literal and `conversation_id` JSON field to be
present in the same file, even though routes and payload types are separated by
the project's package structure.

## Local verification

All of the following completed successfully on the local machine:

```text
go test ./...
go test -race ./internal/service -run 'Test(Availability|BuildAvailability|AccountTest)' -count=1
pnpm run lint:check
pnpm run typecheck
pnpm run check:i18n
pnpm run test:run
pnpm run build
python3 tests/availability-v2/verify_contract.py
git diff --check
```

Frontend result: 270 test files and 1998 tests passed. The production frontend
bundle was built locally; no build or test was run on the production server.

## Remaining release gates

- The strict offline contract gate still intentionally requires an externally
  supplied sanitized fixture from an isolated integration run:
  `python3 tests/availability-v2/verify_contract.py --fixture <path> --require-v2`.
  No fixture was fabricated from the embedded sample.
- Migration 239 and the raw PostgreSQL repository compiled and their existing
  migration tests passed, but this machine did not have a disposable PostgreSQL
  runtime available for a real repository/HTTP integration run.
- The in-process worker map and SSE fan-out do not provide a distributed claim
  lease. Before a multi-instance rollout, add or validate a database-backed
  worker claim so two instances cannot recover the same running turn.
- No production server, production account, real upstream, push, image build,
  deployment, or configuration change was performed during integration.

The branch is suitable for review and isolated staging validation, but the two
items above remain release gates rather than being represented as passed.
