// Only a single small financial snapshot is retained. This cache has no file or
// media buffers; authorization remains the caller's responsibility.
export function createSnapshotReader(build, { ttlMs = 15_000, now = Date.now } = {}) {
  let cached;
  let pending;
  return function readSnapshot() {
    if (cached && now() - cached.at < ttlMs) return Promise.resolve(cached.value);
    if (!pending) {
      pending = Promise.resolve().then(build).then((value) => {
        cached = { at: now(), value };
        return value;
      }).finally(() => { pending = undefined; });
    }
    return pending;
  };
}
