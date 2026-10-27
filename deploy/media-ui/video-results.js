(() => {
  "use strict";

  const unavailableMessage = "视频已生成，但播放文件暂时读取失败。请点击“重新读取视频”，无需重新生成。";

  function playbackUrl(value, origin = globalThis.location?.origin || "https://api.meteor21c.fun") {
    if (typeof value !== "string" || !/^https?:\/\//i.test(value.trim())) return "";
    try {
      const url = new URL(value.trim());
      if (url.username || url.password) return "";
      // These same-origin API endpoints need an Authorization header. A video
      // element cannot attach it, so obtain a signed URL or an authorized Blob.
      if (url.origin === origin && /^\/(?:v1\/)?videos\/(?:[^/]+\/)?[^/]+\/content\/?$/.test(url.pathname)) return "";
      return url.toString();
    } catch { return ""; }
  }

  function responseUrl(data, origin) {
    const pending = [data];
    const seen = new Set();
    for (let i = 0; i < pending.length && i < 16; i += 1) {
      const row = pending[i];
      if (!row || typeof row !== "object" || seen.has(row)) continue;
      seen.add(row);
      for (const value of [row.result_url, row.video_url, row.url, row.video?.url, row.result?.url, row.content?.video_url]) {
        const url = playbackUrl(value, origin);
        if (url) return url;
      }
      for (const key of ["data", "task", "structuredContent", "raw"]) {
        if (row[key] && typeof row[key] === "object") pending.push(row[key]);
      }
    }
    return "";
  }

  async function resolvePlayback(data, { origin, getSignedResult, fetchContent, urlApi = globalThis.URL } = {}) {
    let url = responseUrl(data, origin);
    if (url) return { url, error: "" };
    // This read-only lookup authenticates the existing task before returning
    // its short-lived delivery URL. It never creates another video.
    try {
      if (getSignedResult) url = responseUrl(await getSignedResult(), origin);
      if (url) return { url, error: "" };
    } catch { /* The authorized content endpoint remains available as fallback. */ }
    try {
      const blob = await fetchContent?.();
      if (blob?.size && (blob.type.startsWith("video/") || blob.type === "application/octet-stream")) {
        return { url: urlApi.createObjectURL(blob), error: "" };
      }
    } catch { /* Preserve task success and expose a retryable delivery error. */ }
    return { url: "", error: unavailableMessage };
  }

  globalThis.MeteorMediaVideoResults = { playbackUrl, responseUrl, resolvePlayback, unavailableMessage };
})();
