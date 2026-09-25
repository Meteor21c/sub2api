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

  function tokenQuote(raw) {
    if (!raw || raw.billing_mode !== "token") return null;
    const officialPerMillion = nonnegative(raw.official_per_million);
    const upstreamPerMillion = nonnegative(raw.upstream_per_million);
    const salePerMillion = nonnegative(raw.sale_per_million);
    const prechargeAmount = nonnegative(raw.precharge_amount);
    const saleRateToOfficial = nonnegative(raw.sale_rate_to_official);
    if ([officialPerMillion, upstreamPerMillion, salePerMillion, prechargeAmount, saleRateToOfficial].some(item => item === null)) return null;
    return {
      mode: "token", currency: raw.currency || "CNY", sceneName: String(raw.scene_name || ""),
      officialPerMillion, upstreamPerMillion, salePerMillion, prechargeAmount,
      saleRateToOfficial, providerDiscountRate: nonnegative(raw.provider_discount_rate),
    };
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

  globalThis.MeteorVideoPricing = { money, providerMoney, tokenQuote, multiplier, quote, discount, settlement, tokenUsage };
})();
