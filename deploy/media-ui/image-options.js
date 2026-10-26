(() => {
  const SIZES = Object.freeze({
    '1K': Object.freeze({
      square: '1024x1024',
      landscape: '1024x576',
      portrait: '576x1024',
    }),
    '2K': Object.freeze({
      square: '2048x2048',
      landscape: '2048x1152',
      portrait: '1152x2048',
    }),
    '4K': Object.freeze({
      landscape: '3840x2160',
      portrait: '2160x3840',
    }),
  });
  const TIER_ORDER = Object.freeze(['1K', '2K', '4K']);
  const ORIENTATION_LABELS = Object.freeze({
    square: '正方形',
    landscape: '横屏 · 16:9',
    portrait: '竖屏 · 9:16',
  });

  function normalizedPrice(value) {
    if (value === null || value === undefined || value === '') return null;
    const price = Number(value);
    return Number.isFinite(price) && price >= 0 ? price : null;
  }

  function tiersForGroup(group) {
    if (!group) return [];
    const configured = TIER_ORDER.filter(tier => normalizedPrice(group.imagePrices?.[tier]) !== null);
    if (configured.length) return configured;
    return group.allowImageGeneration ? ['1K', '2K'] : [];
  }

  function orientationsForTier(tier) {
    return Object.keys(SIZES[tier] || {});
  }

  function resolveSize(tier, orientation) {
    return SIZES[tier]?.[orientation] || '';
  }

  function formatPrice(value) {
    const price = normalizedPrice(value);
    if (price === null) return '';
    return `$${price.toFixed(8).replace(/0+$/, '').replace(/\.$/, '')}`;
  }

  globalThis.MeteorImageOptions = {
    ORIENTATION_LABELS,
    tiersForGroup,
    orientationsForTier,
    resolveSize,
    formatPrice,
  };
})();
