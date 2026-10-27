## Summary

- Add an owner-only business monitoring page with daily, weekly, monthly and yearly reports.
- Sample the existing balance endpoint every five minutes without model requests.
- Store rate changes, recharge/check-in candidates and persistent daily summaries.
- Compare local recorded revenue and estimated costs with covered balance movements using a dynamic tolerance.
- Preserve unknown values, first-day coverage and historical revenue during manual credit corrections.
- Build and validate the deployment image in GitHub Actions; production only pulls and recreates the Sub2API service.

## Validation

GitHub Actions runs financial calculation and authorization tests, isolated PostgreSQL integration, idempotent migration validation, frontend type checking and frontend financial-display tests before publishing the image.

## Operations

The feature is disabled until `BUSINESS_MONITOR_OWNER_ID` is configured. Only that administrator's JWT session can access it. Shared admin API keys are rejected.

Database changes are confined to new `business_monitor_*` tables. Daily summaries are retained; raw balance samples use a bounded 90-day retention policy. Existing usage and payment tables are read only.

Historical usage is backfilled one day per minute. Historical balances cannot be reconstructed. Reports distinguish covered balance estimates from complete local usage totals.
