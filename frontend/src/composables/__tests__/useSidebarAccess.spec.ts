import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, reactive, type EffectScope } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { useSidebarAccess } from '../useSidebarAccess'

const mocks = vi.hoisted(() => ({
  auth: null as unknown as { user: { id: number; role: string } | null },
  studio: vi.fn(),
  business: vi.fn()
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('@/api/canvasPilot', () => ({ canvasPilotAPI: { getAccess: mocks.studio } }))
vi.mock('@/api/admin/businessMonitor', () => ({ businessMonitorAPI: { getAccess: mocks.business } }))

const allowed = { enabled: true, allowed: true }
const denied = { enabled: true, allowed: false }
let scopes: EffectScope[] = []

function mount() {
  const scope = effectScope()
  scopes.push(scope)
  return { scope, state: scope.run(() => useSidebarAccess())! }
}

function pending() {
  let resolve!: (value: typeof allowed) => void
  const promise = new Promise<typeof allowed>(done => { resolve = done })
  return { promise, resolve }
}

async function prime() {
  const instance = mount()
  await flushPromises()
  expect(instance.state.canvasPilotAllowed.value).toBe(true)
  expect(instance.state.businessMonitorAllowed.value).toBe(true)
  instance.scope.stop()
}

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  mocks.auth = reactive({ user: { id: 7, role: 'admin' } })
  mocks.studio.mockResolvedValue(allowed)
  mocks.business.mockResolvedValue(allowed)
})
afterEach(() => {
  scopes.forEach(scope => scope.stop())
  scopes = []
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('account-scoped sidebar access', () => {
  it('restores verified menus synchronously while refreshed permissions are pending', async () => {
    await prime()
    mocks.studio.mockReturnValue(pending().promise)
    mocks.business.mockReturnValue(pending().promise)
    const { state } = mount()
    expect(state.canvasPilotAllowed.value).toBe(true)
    expect(state.businessMonitorAllowed.value).toBe(true)
  })

  it('preserves verified menus during temporary network or server failures', async () => {
    await prime()
    mocks.studio.mockRejectedValue({ status: 503 })
    mocks.business.mockRejectedValue(new Error('Offline'))
    const { state } = mount()
    await flushPromises()
    expect(state.canvasPilotAllowed.value).toBe(true)
    expect(state.businessMonitorAllowed.value).toBe(true)
  })

  it('applies revocation and preserves the denied result on the next reload', async () => {
    await prime()
    mocks.studio.mockResolvedValue(denied)
    mocks.business.mockRejectedValue({ status: 403 })
    const instance = mount()
    await flushPromises()
    expect(instance.state.canvasPilotAllowed.value).toBe(false)
    expect(instance.state.businessMonitorAllowed.value).toBe(false)
    instance.scope.stop()
    mocks.studio.mockReturnValue(pending().promise)
    mocks.business.mockReturnValue(pending().promise)
    const { state } = mount()
    expect(state.canvasPilotAllowed.value).toBe(false)
    expect(state.businessMonitorAllowed.value).toBe(false)
  })

  it('ignores late responses after switching to another account and back', async () => {
    const oldStudio = pending()
    const oldBusiness = pending()
    mocks.studio.mockReturnValue(oldStudio.promise)
    mocks.business.mockReturnValue(oldBusiness.promise)
    const { state } = mount()
    mocks.studio.mockReturnValue(pending().promise)
    mocks.business.mockReturnValue(pending().promise)
    mocks.auth.user = { id: 8, role: 'user' }
    expect(state.canvasPilotAllowed.value).toBe(false)
    expect(state.businessMonitorAllowed.value).toBe(false)
    mocks.auth.user = { id: 7, role: 'admin' }
    oldStudio.resolve(allowed)
    oldBusiness.resolve(allowed)
    await flushPromises()
    expect(state.canvasPilotAllowed.value).toBe(false)
    expect(state.businessMonitorAllowed.value).toBe(false)
    expect(localStorage.length).toBe(0)
  })

  it('does not reset menus or refetch permissions when only account data refreshes', async () => {
    const { state } = mount()
    await flushPromises()
    mocks.auth.user = { id: 7, role: 'admin' }
    await flushPromises()
    expect(state.canvasPilotAllowed.value).toBe(true)
    expect(state.businessMonitorAllowed.value).toBe(true)
    expect(mocks.studio).toHaveBeenCalledTimes(1)
    expect(mocks.business).toHaveBeenCalledTimes(1)
  })

  it('does not show an administrator menu under a regular account or after logout', async () => {
    await prime()
    mocks.studio.mockReturnValue(pending().promise)
    mocks.business.mockReturnValue(pending().promise)
    mocks.auth.user = { id: 8, role: 'user' }
    const { state } = mount()
    expect(state.canvasPilotAllowed.value).toBe(false)
    expect(state.businessMonitorAllowed.value).toBe(false)
    expect(mocks.business).toHaveBeenCalledTimes(1)
    mocks.auth.user = null
    expect(state.canvasPilotAllowed.value).toBe(false)
    expect(state.businessMonitorAllowed.value).toBe(false)
  })

  it('expires old navigation hints and tolerates corrupt browser storage', async () => {
    await prime()
    const now = Date.now()
    vi.spyOn(Date, 'now').mockReturnValue(now + 25 * 60 * 60 * 1000)
    mocks.studio.mockReturnValue(pending().promise)
    mocks.business.mockReturnValue(pending().promise)
    const expired = mount()
    expect(expired.state.canvasPilotAllowed.value).toBe(false)
    expect(expired.state.businessMonitorAllowed.value).toBe(false)
    expired.scope.stop()
    localStorage.setItem('sidebar_access_v1', '{')
    expect(() => mount()).not.toThrow()
  })
})
