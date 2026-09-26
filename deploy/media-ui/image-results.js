/* Normalize image results from native Gemini and OpenAI-compatible APIs. */
(() => {
  "use strict";

  function rows(response) {
    // Gemini's native generateContent branch wraps its already-normalized rows
    // directly in `data`; OpenAI-compatible endpoints wrap them in `data.data`.
    if (Array.isArray(response?.data)) return response.data;
    if (Array.isArray(response?.data?.data)) return response.data.data;
    return [];
  }

  window.MeteorMediaImageResults = { rows };
})();
