import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ModelTestsView from '../ModelTestsView.vue'
import zh from '@/i18n/locales/zh'
import type { ModelTestJob } from '@/api/modelTests'

const mock = vi.hoisted(() => ({
  overview: vi.fn(), mine: vi.fn(), keys: vi.fn(), models: vi.fn(), detail: vi.fn(), submit: vi.fn(),
  groups: vi.fn(), savePlan: vi.fn(), deletePlan: vi.fn(), getCatalog: vi.fn(), showError: vi.fn(), showSuccess: vi.fn(),
  route: { path: '/model-tests', fullPath: '/model-tests', query: {} as Record<string, string> }, auth: { isAdmin: false }
}))
vi.mock('@/api/modelTests', () => ({ modelTestsAPI: mock }))
vi.mock('@/api/admin/availability', () => ({ getCatalog: mock.getCatalog, default: { getCatalog: mock.getCatalog } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mock.auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mock.showError, showSuccess: mock.showSuccess }) }))
vi.mock('vue-router', () => ({ useRoute: () => mock.route, useRouter: () => ({ replace: vi.fn() }), RouterLink: { template: '<a><slot /></a>' } }))

const job: ModelTestJob = {
  id: 'b465c8ba-2fe8-4273-b341-2d62b8228eb4', source: 'user', label: 'Pro', model: 'test-model', test_kind: 'pelican',
  status: 'succeeded', verdict: 'ungraded', answer: null, total_ms: 1300, input_tokens: 10, output_tokens: 100,
  created_at: new Date().toISOString(), started_at: new Date().toISOString(), completed_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 86400000).toISOString()
}
function render() {
  return mount(ModelTestsView, { global: {
    plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })],
    stubs: { AppLayout: { template: '<main><slot /></main>' }, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' } }
  } })
}
beforeEach(() => {
  vi.clearAllMocks()
  mock.auth.isAdmin = false
  mock.route.path = '/model-tests'; mock.route.fullPath = '/model-tests'; mock.route.query = {}
  mock.overview.mockResolvedValue({ plans: [], jobs: [], interval_hours: 2, retention_hours: 24 })
  mock.mine.mockResolvedValue([])
  mock.keys.mockResolvedValue([{ id: 2, name: 'My key', group_name: 'Pro' }])
  mock.models.mockResolvedValue(['test-model'])
  mock.submit.mockResolvedValue({ ...job, status: 'queued' })
  mock.groups.mockResolvedValue([{ id: 12, name: 'Pro' }])
  mock.getCatalog.mockResolvedValue({ models: [{ id: 'test-model', downstream_allowed: true }] })
})

describe('ModelTestsView', () => {
  it('shows the 2h/24h policy without granting normal users schedule management', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('每 2 小时自动测试')
    expect(wrapper.text()).toContain('24 小时')
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('上游额度')
    wrapper.unmount()
  })

  it('submits only a key ID and a fixed test, then recovers history after remount', async () => {
    mock.route.query = { tab: 'mine' }
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('select')[0]!.setValue('2'); await flushPromises()
    expect(mock.models).toHaveBeenCalledWith(2)
    await wrapper.find('form').trigger('submit'); await flushPromises()
    expect(mock.submit).toHaveBeenCalledWith(expect.objectContaining({ api_key_id: 2, model: 'test-model', test_kind: 'pelican', client_request_id: expect.any(String) }))
    const payload = mock.submit.mock.calls[0]![0]
    expect(Object.keys(payload).sort()).toEqual(['api_key_id', 'client_request_id', 'model', 'test_kind'])
    wrapper.unmount()
    mock.mine.mockResolvedValue([{ ...job, status: 'running' }])
    const restored = render(); await flushPromises()
    expect(restored.text()).toContain('1 项测试正在进行')
    expect(restored.text()).toContain('test-model')
    expect(mock.submit).toHaveBeenCalledTimes(1)
    restored.unmount()
  })

  it('renders a safe SVG image and keeps untrusted markup as text', async () => {
    mock.route.query = { tab: 'mine' }
    mock.mine.mockResolvedValue([job])
    mock.detail.mockResolvedValue({ ...job, content: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 960 600"><script>alert(1)</script><rect width="960" height="600" fill="red"/></svg>' })
    const wrapper = render(); await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text().includes('鹈鹕动画') && b.text().includes('Pro'))!
    await button.trigger('click'); await flushPromises()
    const image = wrapper.get('[data-testid="model-test-svg"]')
    const src = image.attributes('src')
    expect(decodeURIComponent(src)).toContain('viewBox="0 0 960 600"')
    expect(decodeURIComponent(src)).not.toContain('<script')
    expect(wrapper.findAll('script')).toHaveLength(0)
    expect(mock.detail).toHaveBeenCalledWith(job.id, 'user')
    wrapper.unmount()
  })

  it('allows administrators to save a paused plan without launching a test', async () => {
    mock.auth.isAdmin = true; mock.route.path = '/admin/model-tests'
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '添加计划')!.trigger('click'); await flushPromises()
    await wrapper.find('input[maxlength="80"]').setValue('Plus')
    await wrapper.findAll('select')[0]!.setValue('12'); await flushPromises()
    await wrapper.find('input[maxlength="200"]').setValue('test-model')
    await wrapper.find('form').trigger('submit'); await flushPromises()
    expect(mock.savePlan).toHaveBeenCalledWith(null, { label: 'Plus', group_id: 12, model: 'test-model', test_kind: 'pelican', enabled: false })
    expect(mock.submit).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
