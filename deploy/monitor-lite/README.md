# Secured lightweight balance monitor

Build this standalone runtime on GitHub Actions; no build on production. This
package includes the current lightweight monitor adapters and its existing secret
file loader. The service remains read-only and does not call a model.

## Access

- `MONITOR_SNAPSHOT_TOKEN`: new private service bearer, supplied by protected env
  or `/run/secrets/monitor_snapshot_token`. Missing token fails closed (503).
- `/api/snapshot` and `/api/newapi/snapshot` require that Bearer (401 otherwise).
  Query-string credentials and alert-cron credentials do not grant access.
- `/` redirects to the existing main-site owner session at
  `https://api.meteor21c.fun/admin/business-monitor`; only the configured owner
  JWT can read its balance panel. No service token goes to browser code.
- `/healthz` is safe for unauthenticated health probes.
- Concurrent authorized refreshes coalesce and reuse a 15-second in-process
  snapshot. This small JSON cache has no image/video content.

## Safe rollout order

1. Back up compose/env/database; retain old main and monitor images/releases.
2. Generate a new random service token on the server, without printing it. Put
   the same value in main `BUSINESS_MONITOR_SNAPSHOT_TOKEN` and monitor
   `MONITOR_SNAPSHOT_TOKEN` (or its optional secret file), protected mode0600.
3. Attach monitor to the main Docker network so `meteor-monitor` resolves;
   set main `BUSINESS_MONITOR_SNAPSHOT_URL=http://meteor-monitor:3100/api/snapshot`.
   This operator-only hostname is already allowlisted by the main client.
4. Pull and deploy the main image first: it sends the Bearer to both the old
   public monitor and the new protected monitor. Verify sampling/owner balances.
5. Pull and deploy `ghcr.io/meteor21c/sub2api:monitor-<commit>`; remove the old
   `/app/lite` and `/usr/local/bin/meteor-monitor-entrypoint` source bind mounts
   so they cannot override the immutable source. Keep all current secret mounts
   and other environment values. Retain loopback3101,128MiB,0.25CPU,PIDs64,
   read-only rootfs,8MiB tmpfs,no-new-privileges and cap_drop ALL. Keep current UID
   until secret-file permissions have been separately reviewed.
6. Verify anonymous snapshot401, authenticated internal snapshot200, owner main
   balances200, other admins/users/shared admin keys denied, and natural sampler
   observations continue. No payment or paid generation tests are needed.

On a monitor rollback, the old source is public: keep Nginx's public snapshot
locations denied until a secured image is restored. Do not roll back main until
its sampler credential compatibility has been checked.

## Tests

`node --test lite/test/*.test.mjs` uses isolated local children with no upstream
credentials. It checks auth boundaries, no query auth, missing config fail-closed,
owner redirect, cache coalescing and readiness. The main Go suite separately
checks owner JWT restrictions, service Authorization, no redirect forwarding,
endpoint allowlist, size bounds and filtering of unknown/private fields.
