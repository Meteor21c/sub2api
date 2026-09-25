import test from 'node:test'
import assert from 'node:assert/strict'

const values = new Map([['auth_user', JSON.stringify({ id: 1 })]])
globalThis.window = {
  localStorage: {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  },
}
await import('../media-history.js')

test('video quote, final balance cost, discount and upstream tokens survive page reload', () => {
  const history = globalThis.MeteorMediaHistory
  const scope = history.userScope()
  history.upsertVideo({
    id: 'cgt-test-20260925', model: 'doubao-seedance-2.0-mini',
    createdAt: Date.now(), status: 'success', keyId: '123',
    quote: { unitPrice: 0.05, duration: 4, rate: 0.8, standardCost: 0.2, estimatedCost: 0.16 },
    settlement: { standardCost: 0.2, actualCost: 0.16, effectiveRate: 0.8, discount: '8 折（0.80×）', balanceAfter: 12.34 },
    tokenUsage: { promptTokens: null, completionTokens: 40594, totalTokens: 40594 },
  }, scope)
  const [record] = history.readVideos(scope)
  assert.equal(record.quote.estimatedCost, 0.16)
  assert.equal(record.settlement.actualCost, 0.16)
  assert.equal(record.settlement.discount, '8 折（0.80×）')
  assert.equal(record.settlement.balanceAfter, 12.34)
  assert.equal(record.tokenUsage.completionTokens, 40594)
  assert.equal(JSON.stringify(record).includes('sk-'), false)
})
