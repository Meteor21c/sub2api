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
    if (rate < 1) return `${(rate * 10).toFixed(2).replace(/0+$/, "").replace(/\.$/, "")} 折（${rate.toFixed(2)}×）`;
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

  globalThis.MeteorVideoPricing = { money, multiplier, quote, discount, settlement, tokenUsage };
})();
