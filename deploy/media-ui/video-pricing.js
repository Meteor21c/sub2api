/* Browser-side display only. Final charges always come from Sub2API usage logs. */
(() => {
  "use strict";

  function nonnegative(value) {
    if (value === null || value === undefined || value === "") return null;
    const number = Number(value);
    return Number.isFinite(number) && number >= 0 ? number : null;
  }

  function money(value) {
    const number = nonnegative(value);
    if (number === null) return "—";
    return `$${number.toFixed(6).replace(/(\.\d{2,}?)0+$/, "$1")}`;
  }

  function providerMoney(value, currency = "CNY") {
    const number = nonnegative(value);
    if (number === null) return "—";
    return `${currency === "CNY" ? "¥" : "$"}${number.toFixed(6).replace(/(\.\d{2,}?)0+$/, "$1")}`;
  }

  const PROVIDER_SOURCE = "fzyinghe-marketplace-by-api-key";
  const isProviderModel = model => /^doubao-seedance-/.test(String(model)) || ["kling-v3", "kling-v3-omni"].includes(model);
  const resolutionName = value => /^4k$/i.test(String(value)) ? "4K" : String(value || "").toLowerCase();
  const validResolutions = values => [...new Set((Array.isArray(values) ? values : []).map(resolutionName).filter(value => ["480p", "720p", "1080p", "4K"].includes(value)))];
  const fetchedAt = value => typeof value === "string" && Number.isFinite(Date.parse(value)) ? new Date(value).toISOString() : "";

  function providerQuote(raw) {
    if (!raw || !["token", "second"].includes(raw.billing_mode)) return null;
    const mode = raw.billing_mode;
    const official = nonnegative(mode === "token" ? raw.official_per_million : raw.official_per_second);
    const upstream = nonnegative(mode === "token" ? raw.upstream_per_million : raw.upstream_per_second);
    const sale = nonnegative(mode === "token" ? raw.sale_per_million : raw.sale_per_second);
    const prechargeAmount = nonnegative(raw.precharge_amount);
    const saleRateToOfficial = nonnegative(raw.sale_rate_to_official);
    const duration = nonnegative(raw.duration);
    if ([official, upstream, sale, prechargeAmount, saleRateToOfficial].some(item => item === null) ||
        (mode === "second" && (!duration || !Number.isInteger(duration)))) return null;
    return {
      mode, currency: raw.currency === "USD" ? "USD" : "CNY", sceneName: String(raw.scene_name || "").slice(0, 160),
      ...(mode === "token" ? { officialPerMillion: official, upstreamPerMillion: upstream, salePerMillion: sale }
        : { officialPerSecond: official, upstreamPerSecond: upstream, salePerSecond: sale, duration }),
      prechargeAmount, source: raw.source === PROVIDER_SOURCE ? PROVIDER_SOURCE : "", fetchedAt: fetchedAt(raw.fetched_at),
      saleRateToOfficial, providerDiscountRate: nonnegative(raw.provider_discount_rate),
    };
  }

  // Retained for older callers. New UI accepts both provider billing units.
  const tokenQuote = raw => raw?.billing_mode === "token" ? providerQuote(raw) : null;

  function providerCatalog(raw, model) {
    if (raw?.source !== PROVIDER_SOURCE || raw.model !== model || !Array.isArray(raw.scenarios)) return null;
    const resolutions = validResolutions(raw.resolutions);
    const scenarios = raw.scenarios.filter(row => row && ["token", "second"].includes(row.billing_mode))
      .map(row => ({
        resolutions: validResolutions(row.resolutions),
        ...(typeof row.audio === "boolean" ? { audio: row.audio } : {}),
        ...(typeof row.input_video === "boolean" ? { inputVideo: row.input_video } : {}),
      })).filter(row => row.resolutions.length);
    return resolutions.length && scenarios.length ? { model, source: PROVIDER_SOURCE, fetchedAt: fetchedAt(raw.fetched_at), resolutions, scenarios } : null;
  }

  function scenarioResolutions(catalog, { audio, inputVideo } = {}) {
    if (!catalog) return [];
    const matches = catalog.scenarios.filter(row =>
      (typeof row.audio !== "boolean" || row.audio === audio) &&
      (typeof row.inputVideo !== "boolean" || row.inputVideo === inputVideo));
    const selected = new Set(matches.flatMap(row => row.resolutions));
    return catalog.resolutions.filter(value => selected.has(value));
  }

  function providerDirectory(raw) {
    if (raw?.source !== PROVIDER_SOURCE || !Array.isArray(raw.models) || !Array.isArray(raw.available_models)) return null;
    const models = Object.fromEntries(raw.models.filter(row => isProviderModel(row?.model))
      .map(row => [row.model, providerCatalog({ source: raw.source, fetched_at: raw.fetched_at, ...row }, row.model)])
      .filter(([, catalog]) => catalog));
    const availableModels = raw.available_models.filter(model => isProviderModel(model) && models[model]);
    return availableModels.length ? { models, availableModels } : null;
  }

  function selectionKey({ keyId, model, resolution, duration, audio, inputVideo }) {
    return JSON.stringify([String(keyId || ""), String(model || ""), resolutionName(resolution), Number(duration), !!audio, !!inputVideo]);
  }

  function referenceHasVideo(values) {
    return (Array.isArray(values) ? values : []).some(value => {
      try {
        if (/^video:https?:\/\//i.test(String(value).trim())) return true;
        const raw = String(value).trim().replace(/^[^:]+:(?=https?:\/\/)/i, "");
        const url = new URL(raw);
        return /\.(mp4|mov|webm|m4v)$/i.test(url.searchParams.get("name") || url.searchParams.get("filename") || url.pathname);
      } catch { return false; }
    });
  }

  function multiplier(group) {
    if (!group) return null;
    if (group.videoRateIndependent) return nonnegative(group.videoRateMultiplier);
    return nonnegative(group.userRateMultiplier) ?? nonnegative(group.rateMultiplier);
  }

  function quote(group, model, resolution, duration) {
    const seconds = Number(duration);
    if (!group || !model || !Number.isInteger(seconds) || seconds <= 0) return null;
    // The current Sub2API video billing normalizer supports at most 15 seconds
    // and only these three resolution tiers. Do not imply an exact quote for
    // longer Seedance 2.5 clips or Kling 4K until backend pricing supports them.
    if (seconds > 15 || !["480p", "720p", "1080p"].includes(resolution)) return null;
    const unitPrice = nonnegative(group.videoPrices?.[model]?.[resolution])
      ?? nonnegative(group.videoFallbackPrices?.[resolution]);
    const rate = multiplier(group);
    if (unitPrice === null || rate === null) return null;
    return {
      unitPrice, duration: seconds, rate,
      standardCost: Number((unitPrice * seconds).toFixed(8)),
      estimatedCost: Number((unitPrice * seconds * rate).toFixed(8)),
    };
  }

  function discount(value) {
    const rate = nonnegative(value);
    if (rate === null) return "未提供";
    if (Math.abs(rate - 1) < 0.000001) return "无折扣（1.00×）";
    if (rate < 1) {
      const folds = Number((rate * 10).toFixed(4));
      const multiplierText = Math.abs(rate * 100 - Math.round(rate * 100)) < 1e-8
        ? rate.toFixed(2) : rate.toFixed(4).replace(/0+$/, "");
      return `${folds} 折（${multiplierText}×）`;
    }
    return `加价 ${((rate - 1) * 100).toFixed(1)}%（${rate.toFixed(2)}×）`;
  }

  function settlement(log) {
    if (!log || typeof log !== "object") return null;
    const standardCost = nonnegative(log.total_cost);
    const actualCost = nonnegative(log.actual_cost);
    if (actualCost === null) return null;
    const effectiveRate = standardCost !== null && standardCost > 0
      ? actualCost / standardCost : nonnegative(log.rate_multiplier);
    return { standardCost, actualCost, effectiveRate, discount: discount(effectiveRate) };
  }

  function tokenUsage(value) {
    if (!value || typeof value !== "object") return null;
    const completionTokens = nonnegative(value.completion_tokens);
    const totalTokens = nonnegative(value.total_tokens);
    const promptTokens = nonnegative(value.prompt_tokens);
    if ([completionTokens, totalTokens, promptTokens].every((item) => item === null)) return null;
    return { completionTokens, totalTokens, promptTokens };
  }

  globalThis.MeteorVideoPricing = { money, providerMoney, tokenQuote, providerQuote, providerCatalog, providerDirectory, scenarioResolutions,
    selectionKey, referenceHasVideo, isProviderModel, multiplier, quote, discount, settlement, tokenUsage };
})();
