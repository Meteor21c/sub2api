import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BusinessMonitorView from '../BusinessMonitorView.vue'

const mocks = vi.hoisted(() => ({ getReport: vi.fn(), getBalances: vi.fn() }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/api/admin/businessMonitor', () => ({ businessMonitorAPI: mocks }))

const report = {
  period: 'day', date: '2026-10-28', start: '2026-10-28T00:00:00+08:00', end: '2026-10-29T00:00:00+08:00',
  as_of: '2026-10-28T01:00:00+08:00', finalized: false, coverage_start: null,
  summary: { partial: false, revenue: 0, subscription_usage: 0, excluded_media_revenue: 0, excluded_media_requests: 0, expected_cost: 0, gross_profit: 0, balance_cost: null, observed_profit: null, deviation: null, anomaly_count: 0 },
  wallets: [], daily: [], rate_changes: [], credits: []
}

beforeEach(() => {
  mocks.getReport.mockReset().mockResolvedValue(report)
  mocks.getBalances.mockReset()
})

describe('owner balance panel', () => {
  it('shows original currencies and keeps true zero distinct from unavailable balance', async () => {
    mocks.getBalances.mockResolvedValue({ generatedAt: '2026-10-28T01:00:00+08:00', accounts: [
      { id: 'zero', name: 'Zero supplier', baseUrl: 'https://zero.example.test', balance: 0, status: 'low', currency: '$', planName: 'wallet', threshold: 10 },
      { id: 'waiting', name: 'Waiting supplier', baseUrl: 'https://waiting.example.test', balance: null, status: 'waiting', currency: '¥', planName: '', threshold: 10 }
    ] })
    const wrapper = mount(BusinessMonitorView)
    await flushPromises()
    expect(wrapper.text()).toContain('仅站点所有者可见')
    expect(wrapper.text()).toContain('Zero supplier')
    expect(wrapper.text()).toContain('$0.00')
    expect(wrapper.text()).toContain('预警线 $10.00')
    expect(wrapper.text()).toContain('等待凭据')
    expect(wrapper.findAll('article').some(card => card.text().includes('Waiting supplier') && card.text().includes('—'))).toBe(true)
    wrapper.unmount()
  })

  it('keeps the historical report usable when fresh balance access fails', async () => {
    mocks.getBalances.mockRejectedValue(new Error('上游余额暂时不可用'))
    const wrapper = mount(BusinessMonitorView)
    await flushPromises()
    expect(wrapper.text()).toContain('上游余额暂时不可用')
    expect(wrapper.text()).toContain('每日经营汇总')
    expect(wrapper.text()).toContain('计入客户消费额')
    wrapper.unmount()
  })
})
