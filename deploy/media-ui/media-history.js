/* Browser-local media history. No API key or login token is stored here. */
(() => {
  "use strict";

  const DB_NAME = "meteor-media-history-v1";
  const DB_VERSION = 1;
  const IMAGE_STORE = "images";
  const VIDEO_STORAGE_PREFIX = "meteor-media:video-history:v1";
  const RESULT_TTL_MS = 24 * 60 * 60 * 1000;
  const PENDING_TTL_MS = 20 * 60 * 1000;
  const HISTORY_LIMIT = 12;
  const MAX_IMAGE_BYTES = 128 * 1024 * 1024;
  const RASTER_TYPES = new Set(["image/png", "image/jpeg", "image/webp", "image/avif"]);

  function browserWindow() {
    return typeof window === "undefined" ? null : window;
  }

  function storage() {
    try {
      return browserWindow()?.localStorage || null;
    } catch {
      return null;
    }
  }

  function userScope() {
    const local = storage();
    if (!local) return "";
    try {
      const user = JSON.parse(local.getItem("auth_user") || "null");
      const id = user?.id;
      if ((typeof id === "number" && Number.isFinite(id)) || (typeof id === "string" && id.trim())) {
        return `user:${String(id).trim()}`;
      }
    } catch {
      // A missing or malformed user record must never fall back to shared history.
    }
    return "";
  }

  function isCurrentScope(scope) {
    return !!scope && scope === userScope();
  }

  function safeRemoteUrl(value) {
    return typeof value === "string" && /^https?:\/\//i.test(value.trim()) ? value.trim() : "";
  }

  function requestResult(request) {
    return new Promise((resolve, reject) => {
      request.addEventListener("success", () => resolve(request.result), { once: true });
      request.addEventListener("error", () => reject(request.error || new Error("IndexedDB request failed")), { once: true });
    });
  }

  function transactionDone(transaction) {
    return new Promise((resolve, reject) => {
      transaction.addEventListener("complete", () => resolve(), { once: true });
      transaction.addEventListener("abort", () => reject(transaction.error || new Error("IndexedDB transaction aborted")), { once: true });
      transaction.addEventListener("error", () => reject(transaction.error || new Error("IndexedDB transaction failed")), { once: true });
    });
  }

  function openDatabase() {
    const indexedDB = browserWindow()?.indexedDB;
    if (!indexedDB) return Promise.reject(new Error("IndexedDB is unavailable"));
    return new Promise((resolve, reject) => {
      const request = indexedDB.open(DB_NAME, DB_VERSION);
      request.addEventListener("upgradeneeded", () => {
        const database = request.result;
        if (!database.objectStoreNames.contains(IMAGE_STORE)) {
          const store = database.createObjectStore(IMAGE_STORE, { keyPath: "key" });
          store.createIndex("scope", "scope", { unique: false });
        }
      });
      request.addEventListener("success", () => resolve(request.result), { once: true });
      request.addEventListener("error", () => reject(request.error || new Error("IndexedDB open failed")), { once: true });
    });
  }

  function decodeBase64(value, fallbackType = "image/png") {
    const comma = value.indexOf(",");
    const payload = comma >= 0 ? value.slice(comma + 1) : value;
    const header = comma >= 0 ? value.slice(0, comma) : "";
    const type = /^data:([^;]+)/.exec(header)?.[1] || fallbackType;
    if (!RASTER_TYPES.has(type.toLowerCase())) throw new Error("Unsupported image type");
    if (payload.length > Math.ceil(MAX_IMAGE_BYTES * 4 / 3) + 4) throw new Error("Image is too large");
    const binary = browserWindow().atob(payload);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
    return new Blob([bytes], { type });
  }

  async function storedImage(row, remainingBytes) {
    const revisedPrompt = typeof row?.revised_prompt === "string" ? row.revised_prompt.trim() : "";
    const base64 = typeof row?.b64_json === "string" ? row.b64_json : "";
    const url = typeof row?.url === "string" ? row.url.trim() : "";
    if (base64) {
      const blob = decodeBase64(base64);
      return blob.size <= remainingBytes ? { blob, ...(revisedPrompt ? { revisedPrompt } : {}) } : null;
    }
    if (url.startsWith("data:")) {
      const blob = decodeBase64(url);
      return blob.size <= remainingBytes ? { blob, ...(revisedPrompt ? { revisedPrompt } : {}) } : null;
    }
    const remoteUrl = safeRemoteUrl(url);
    if (remoteUrl) {
      try {
        const response = await browserWindow().fetch(remoteUrl, { cache: "no-store", credentials: "omit" });
        const declaredBytes = Number(response.headers.get("content-length")) || 0;
        if (!response.ok || declaredBytes > remainingBytes) throw new Error("remote image is unavailable or too large");
        const blob = await response.blob();
        if (!blob.size || blob.size > remainingBytes || !RASTER_TYPES.has(blob.type.toLowerCase())) throw new Error("remote response is not a supported image");
        return { blob, ...(revisedPrompt ? { revisedPrompt } : {}) };
      } catch {
        // Some CDNs block browser CORS. Keep the URL as a best-effort fallback.
      }
    }
    if (remoteUrl) return { url: remoteUrl, ...(revisedPrompt ? { revisedPrompt } : {}) };
    return null;
  }

  async function imageRecords(database, scope) {
    const transaction = database.transaction(IMAGE_STORE, "readonly");
    const done = transactionDone(transaction);
    const result = await requestResult(transaction.objectStore(IMAGE_STORE).index("scope").getAll(scope));
    await done;
    return Array.isArray(result) ? result : [];
  }

  function imageKeysToDelete(records, now = Date.now()) {
    const sorted = [...records].sort((left, right) => Number(right.createdAt) - Number(left.createdAt));
    let retainedBytes = 0;
    const keys = [];
    sorted.forEach((record, index) => {
      const bytes = Math.max(0, Number(record.sizeBytes) || 0);
      const expired = !Number.isFinite(Number(record.createdAt)) || now - Number(record.createdAt) > RESULT_TTL_MS;
      const overCount = index >= HISTORY_LIMIT;
      const overBytes = retainedBytes + bytes > MAX_IMAGE_BYTES;
      if (expired || overCount || overBytes) keys.push(record.key);
      else retainedBytes += bytes;
    });
    return keys;
  }

  async function trimImages(database, scope) {
    const records = await imageRecords(database, scope);
    const keys = imageKeysToDelete(records);
    if (!keys.length) return;
    const transaction = database.transaction(IMAGE_STORE, "readwrite");
    const done = transactionDone(transaction);
    const store = transaction.objectStore(IMAGE_STORE);
    keys.forEach((key) => store.delete(key));
    await done;
  }

  async function saveImages(entry, expectedScope = userScope()) {
    if (!isCurrentScope(expectedScope)) return false;
    const images = [];
    let sizeBytes = 0;
    for (const row of Array.isArray(entry?.images) ? entry.images : []) {
      const image = await storedImage(row, MAX_IMAGE_BYTES - sizeBytes);
      if (!isCurrentScope(expectedScope)) return false;
      if (!image) continue;
      images.push(image);
      sizeBytes += image.blob?.size || 0;
    }
    if (!images.length) return false;
    const createdAt = Number(entry.createdAt) || Date.now();
    const id = String(entry.id || `${createdAt}-${Math.random().toString(36).slice(2, 8)}`);
    const database = await openDatabase();
    try {
      await trimImages(database, expectedScope);
      const transaction = database.transaction(IMAGE_STORE, "readwrite");
      const done = transactionDone(transaction);
      transaction.objectStore(IMAGE_STORE).put({
        key: `${expectedScope}:${id}`,
        scope: expectedScope,
        id,
        createdAt,
        model: String(entry.model || ""),
        prompt: String(entry.prompt || ""),
        images,
        sizeBytes,
      });
      await done;
      await trimImages(database, expectedScope);
      return true;
    } finally {
      database.close();
    }
  }

  async function readImages(expectedScope = userScope()) {
    if (!isCurrentScope(expectedScope)) return { entries: [], objectUrls: [] };
    const database = await openDatabase();
    try {
      await trimImages(database, expectedScope);
      const records = (await imageRecords(database, expectedScope)).sort((left, right) => right.createdAt - left.createdAt);
      const objectUrls = [];
      const entries = records.map((record) => ({
        id: record.id,
        createdAt: record.createdAt,
        model: record.model,
        prompt: record.prompt,
        images: (record.images || []).map((image) => {
          let url = safeRemoteUrl(image.url);
          if (image.blob && RASTER_TYPES.has(String(image.blob.type).toLowerCase())) {
            url = browserWindow().URL.createObjectURL(image.blob);
            objectUrls.push(url);
          }
          return { ...(url ? { url } : {}), ...(image.revisedPrompt ? { revised_prompt: image.revisedPrompt } : {}) };
        }),
      }));
      return { entries, objectUrls };
    } finally {
      database.close();
    }
  }

  async function clearImages(expectedScope = userScope()) {
    if (!isCurrentScope(expectedScope)) return;
    const database = await openDatabase();
    try {
      const records = await imageRecords(database, expectedScope);
      if (!records.length) return;
      const transaction = database.transaction(IMAGE_STORE, "readwrite");
      const done = transactionDone(transaction);
      const store = transaction.objectStore(IMAGE_STORE);
      records.forEach((record) => store.delete(record.key));
      await done;
    } finally {
      database.close();
    }
  }

  function revokeObjectUrls(urls) {
    const urlApi = browserWindow()?.URL;
    if (!urlApi) return;
    (Array.isArray(urls) ? urls : []).forEach((url) => {
      if (typeof url === "string" && url.startsWith("blob:")) urlApi.revokeObjectURL(url);
    });
  }

  function videoStorageKey(scope = userScope()) {
    return scope ? `${VIDEO_STORAGE_PREFIX}:${scope}` : "";
  }

  function pruneVideos(records, now = Date.now()) {
    return (Array.isArray(records) ? records : [])
      .filter((record) => record && Number.isFinite(Number(record.createdAt)) && now - Number(record.createdAt) <= RESULT_TTL_MS)
      .sort((left, right) => Number(right.createdAt) - Number(left.createdAt))
      .slice(0, HISTORY_LIMIT)
      .map((record) => ({
        id: String(record.id || ""),
        createdAt: Number(record.createdAt),
        updatedAt: Number(record.updatedAt) || Number(record.createdAt),
        keyId: String(record.keyId || ""),
        model: String(record.model || ""),
        prompt: String(record.prompt || ""),
        status: ["success", "failure"].includes(record.status) ? record.status : "pending",
        url: safeRemoteUrl(record.url),
        reason: String(record.reason || ""),
      }))
      .filter((record) => record.id);
  }

  function readVideos(expectedScope = userScope()) {
    const local = storage();
    const key = videoStorageKey(expectedScope);
    if (!local || !key || !isCurrentScope(expectedScope)) return [];
    try {
      const records = pruneVideos(JSON.parse(local.getItem(key) || "[]"));
      local.setItem(key, JSON.stringify(records));
      return records;
    } catch {
      return [];
    }
  }

  function writeVideos(records, expectedScope = userScope()) {
    const local = storage();
    const key = videoStorageKey(expectedScope);
    if (!local || !key || !isCurrentScope(expectedScope)) return [];
    const safe = pruneVideos(records);
    try {
      local.setItem(key, JSON.stringify(safe));
    } catch {
      // Quota/private-mode failures must not interrupt generation.
    }
    return safe;
  }

  function upsertVideo(entry, expectedScope = userScope()) {
    if (!entry?.id || !isCurrentScope(expectedScope)) return [];
    const now = Date.now();
    const current = readVideos(expectedScope);
    const existing = current.find((record) => record.id === String(entry.id)) || {};
    const records = current.filter((record) => record.id !== String(entry.id));
    records.unshift({
      id: String(entry.id),
      createdAt: Number(entry.createdAt) || Number(existing.createdAt) || now,
      updatedAt: now,
      keyId: String(entry.keyId ?? existing.keyId ?? ""),
      model: String(entry.model ?? existing.model ?? ""),
      prompt: String(entry.prompt ?? existing.prompt ?? ""),
      status: ["success", "failure"].includes(entry.status) ? entry.status : (entry.status === "pending" ? "pending" : existing.status || "pending"),
      url: typeof entry.url === "string" ? safeRemoteUrl(entry.url) : String(existing.url || ""),
      reason: String(entry.reason ?? existing.reason ?? ""),
    });
    return writeVideos(records, expectedScope);
  }

  function clearVideos(expectedScope = userScope()) {
    const local = storage();
    const key = videoStorageKey(expectedScope);
    if (!local || !key || !isCurrentScope(expectedScope)) return;
    try {
      local.removeItem(key);
    } catch {
      // Storage may be unavailable.
    }
  }

  globalThis.MeteorMediaHistory = {
    RESULT_TTL_MS,
    PENDING_TTL_MS,
    HISTORY_LIMIT,
    MAX_IMAGE_BYTES,
    userScope,
    imageKeysToDelete,
    pruneVideos,
    saveImages,
    readImages,
    clearImages,
    revokeObjectUrls,
    readVideos,
    upsertVideo,
    clearVideos,
  };
})();
