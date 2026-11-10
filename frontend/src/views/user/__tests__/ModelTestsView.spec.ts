import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ModelTestsView from '../ModelTestsView.vue'
import zh from '@/i18n/locales/zh'
import type { AdminModelTestJob, ModelTestJob, ModelTestPlan } from '@/api/modelTests'

const mock = vi.hoisted(() => ({
  overview: vi.fn(), mine: vi.fn(), keys: vi.fn(), models: vi.fn(), detail: vi.fn(), submit: vi.fn(),
  groups: vi.fn(), savePlan: vi.fn(), deletePlan: vi.fn(), getCatalog: vi.fn(), showError: vi.fn(), showSuccess: vi.fn(),
  adminJobs: vi.fn(), adminDetail: vi.fn(), deleteJob: vi.fn(), retryJob: vi.fn(), runPlan: vi.fn(),
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
const plan: ModelTestPlan = { id: 12, label: 'Plus', group_id: 12, model: 'test-model', test_kind: 'pelican', enabled: false, next_run_at: null, last_run_at: null }
const adminJob: AdminModelTestJob = {
  ...job, source: 'scheduled', plan_id: 12, manual: false, retry_of: null,
  upstream_account_id: 42, upstream_account_name: 'Private final channel',
  upstream_attempts: [
    { account_id: 41, account_name: 'Private first channel', success: false, status_code: 503 },
    { account_id: 42, account_name: 'Private final channel', success: true, status_code: 200 }
  ]
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
  mock.adminJobs.mockResolvedValue([])
  mock.adminDetail.mockResolvedValue(adminJob)
  mock.deleteJob.mockResolvedValue(undefined)
  mock.retryJob.mockResolvedValue({ ...adminJob, status: 'queued' })
  mock.runPlan.mockResolvedValue({ ...adminJob, status: 'queued' })
})

describe('ModelTestsView', () => {
  it('shows the 2h/24h policy without granting normal users schedule management', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('每 2 小时自动测试')
    expect(wrapper.text()).toContain('24 小时')
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('上游额度')
    expect(mock.adminJobs).not.toHaveBeenCalled()
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

  it('shows actual channels and failed attempts only through admin endpoints', async () => {
    mock.auth.isAdmin = true; mock.route.path = '/admin/model-tests'
    mock.overview.mockResolvedValue({ plans: [plan], jobs: [{ ...job, source: 'scheduled', plan_id: 12 }], interval_hours: 2, retention_hours: 24 })
    mock.adminJobs.mockResolvedValue([adminJob])
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="admin-test-records"]').text()).toContain('Private final channel')
    await wrapper.get(`[data-testid="view-admin-job-${job.id}"]`).trigger('click'); await flushPromises()
    const detail = wrapper.get('[data-testid="admin-upstream-detail"]')
    expect(detail.text()).toContain('Private first channel')
    expect(detail.text()).toContain('HTTP 503')
    expect(mock.adminDetail).toHaveBeenCalledWith(job.id)
    expect(mock.detail).not.toHaveBeenCalled()
    wrapper.unmount()

    mock.auth.isAdmin = false; mock.route.path = '/model-tests'
    const customer = render(); await flushPromises()
    expect(customer.text()).not.toContain('Private final channel')
    expect(customer.find('[data-testid="admin-test-records"]').exists()).toBe(false)
    customer.unmount()
  })

  it('deletes a confirmed record and refreshes the shared results', async () => {
    mock.auth.isAdmin = true; mock.route.path = '/admin/model-tests'
    mock.adminJobs.mockResolvedValueOnce([adminJob]).mockResolvedValue([])
    const wrapper = render(); await flushPromises()
    await wrapper.get(`[data-testid="delete-job-${job.id}"]`).trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('删除后将同步从用户页面移除')
    expect(mock.deleteJob).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-delete-result"]').trigger('click'); await flushPromises()
    expect(mock.deleteJob).toHaveBeenCalledWith(job.id)
    expect(mock.overview).toHaveBeenCalledTimes(2)
    expect(wrapper.find(`[data-testid="admin-job-${job.id}"]`).exists()).toBe(false)
    wrapper.unmount()
  })

  it('retests with a stable submission UUID across network failures', async () => {
    mock.auth.isAdmin = true; mock.route.path = '/admin/model-tests'
    mock.adminJobs.mockResolvedValue([adminJob])
    mock.retryJob.mockRejectedValueOnce(new Error('network error')).mockResolvedValueOnce({ ...adminJob, status: 'queued' })
    const wrapper = render(); await flushPromises()
    await wrapper.get(`[data-testid="retry-job-${job.id}"]`).trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('原记录保留')
    expect(mock.retryJob).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="confirm-run-test"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="confirm-run-test"]').trigger('click'); await flushPromises()
    expect(mock.retryJob).toHaveBeenCalledTimes(2)
    expect(mock.retryJob.mock.calls[0]).toEqual([job.id, expect.any(String)])
    expect(mock.retryJob.mock.calls[1]).toEqual(mock.retryJob.mock.calls[0])
    expect(mock.submit).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('allows a paused plan to run now and disables unsafe active-job actions', async () => {
    mock.auth.isAdmin = true; mock.route.path = '/admin/model-tests'
    mock.overview.mockResolvedValue({ plans: [plan], jobs: [], interval_hours: 2, retention_hours: 24 })
    const running = { ...adminJob, id: 'running', plan_id: 99, status: 'running' as const, upstream_account_id: null, upstream_account_name: '', upstream_attempts: [] }
    mock.adminJobs.mockResolvedValue([running])
    const wrapper = render(); await flushPromises()
    expect(wrapper.get('[data-testid="retry-job-running"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="delete-job-running"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="run-plan-12"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('不会改变下次定时测试时间')
    await wrapper.get('[data-testid="confirm-run-test"]').trigger('click'); await flushPromises()
    expect(mock.runPlan).toHaveBeenCalledWith(12, expect.any(String))
    expect(mock.savePlan).not.toHaveBeenCalled()
    expect(mock.submit).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('closes an open completed result when the next successful refresh removes it', async () => {
    const publicJob = { ...job, source: 'scheduled' as const, plan_id: 12 }
    mock.overview.mockResolvedValueOnce({ plans: [plan], jobs: [publicJob], interval_hours: 2, retention_hours: 24 })
      .mockResolvedValue({ plans: [plan], jobs: [], interval_hours: 2, retention_hours: 24 })
    mock.detail.mockResolvedValue({ ...publicJob, content: 'old visible response' })
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('查看结果'))!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('old visible response')
    await wrapper.get('button[aria-label="刷新"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).not.toContain('old visible response')
    expect(wrapper.text()).toContain('等待首次测试')
    wrapper.unmount()
  })
})
