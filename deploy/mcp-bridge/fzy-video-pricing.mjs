// The marketplace returns official scenario prices plus the API key's discount.
// Keep this snapshot in provider currency; USD balance conversion belongs to
// Sub2API's billing transaction, using an explicit exchange-rate configuration.
const TOKEN_MARKUP = 1.15

function positiveNumber(value) {
  if (value === null || value === undefined || value === '') return null
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null
}

function resolutionMatches(ruleResolution, requestedResolution) {
  const wanted = String(requestedResolution || '').trim().toLowerCase()
  return String(ruleResolution || '').toLowerCase().split('/').some((part) => part.trim() === wanted)
}

function includesInputVideo(body) {
  return Array.isArray(body?.content) && body.content.some((item) => item?.type === 'video_url')
}

function selectScenario(model, rules, body) {
  const resolution = String(body?.resolution || '720p').trim().toLowerCase()
  const audio = body?.generate_audio !== false
  if (model === 'doubao-seedance-1.5-pro') {
    // This bridge uses the online V3 task endpoint, never offline inference.
    return rules.find((rule) => String(rule.sceneName || '').includes('在线推理') &&
      String(rule.sceneName || '').includes(audio ? '有声' : '无声'))
  }
  const hasVideo = includesInputVideo(body)
  return rules.find((rule) => {
    const mode = String(rule.inputMode || '')
    const matchingInput = hasVideo ? mode.includes('含输入视频') : mode.includes('无输入视频')
    return matchingInput && resolutionMatches(rule.resolution, resolution)
  })
}

export function snapshotFzyVideoPricing(response, model, body) {
  const models = Array.isArray(response?.data) ? response.data : null
  if (response?.code !== 200 || !models) throw new Error('provider marketplace response is invalid')
  const entry = models.find((item) => item?.innerCode === model)
  const rule = entry?.billingRule
  if (!rule || rule.displayMode !== 'VIDEO_SCENARIOS') throw new Error('video pricing is unavailable')
  const discount = positiveNumber(entry.discount)
  if (discount === null) throw new Error('API key discount is unavailable')
  const scenario = selectScenario(model, rule.scenarioRules || [], body)
  const officialPerMillion = positiveNumber(scenario?.outputPricePerMillion)
  const unitSize = positiveNumber(scenario?.tokenUnitSize || rule.tokenUnitSize)
  const currency = scenario?.pricingCurrency || rule.pricingCurrency
  if (officialPerMillion === null || unitSize !== 1000000 || !['CNY', 'USD'].includes(currency)) {
    throw new Error('token-priced video scenario is unavailable')
  }
  if (scenario.pricePerSecond !== null && scenario.pricePerSecond !== undefined) {
    throw new Error('per-second video scenario is not eligible for token billing')
  }
  return {
    source: 'fzyinghe-marketplace-by-api-key',
    model,
    sceneCode: scenario.sceneCode,
    sceneName: scenario.sceneName,
    currency,
    tokenUnitSize: unitSize,
    officialPerMillion,
    discountRate: discount,
    upstreamPerMillion: officialPerMillion * discount,
    markupRate: TOKEN_MARKUP,
    salePerMillion: officialPerMillion * discount * TOKEN_MARKUP,
    saleRateToOfficial: discount * TOKEN_MARKUP,
  }
}
