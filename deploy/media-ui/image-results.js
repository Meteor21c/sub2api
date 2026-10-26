/* Normalize image results from native Gemini and OpenAI-compatible APIs. */
(() => {
  "use strict";

  function displayUrl(row) {
    const base64 = typeof row?.b64_json === "string" ? row.b64_json.trim() : "";
    if (base64) {
      if (base64.startsWith("data:")) return /^data:image\/(?:png|jpeg|webp|avif);/i.test(base64) ? base64 : "";
      return `data:image/png;base64,${base64}`;
    }
    const url = typeof row?.url === "string" ? row.url.trim() : "";
    return /^(?:https?:\/\/|blob:)/i.test(url) ? url : "";
  }

  function rows(response) {
    // Gemini's native generateContent branch wraps its already-normalized rows
    // directly in `data`; OpenAI-compatible endpoints wrap them in `data.data`.
    const values = Array.isArray(response?.data) ? response.data
      : Array.isArray(response?.data?.data) ? response.data.data : [];
    return values.filter((row) => displayUrl(row));
  }

  window.MeteorMediaImageResults = { rows, displayUrl };
})();
