import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChannelTestView from '../ChannelTestView.vue'

const { listAccounts, getAvailableModels, debugTestAccount, showSuccess } = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  getAvailableModels: vi.fn(),
  debugTestAccount: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getAvailableModels,
      debugTestAccount
    }
  }
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

describe('administrator channel test page', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({
      items: [{ id: 7, name: 'test channel', platform: 'openai', type: 'api-key', status: 'active' }],
      total: 1,
      page: 1,
      page_size: 100,
      pages: 1
    })
    getAvailableModels.mockReset().mockResolvedValue([{ id: 'gpt-test', display_name: 'GPT Test' }])
    debugTestAccount.mockReset().mockResolvedValue({
      content: 'hello back',
      model: 'gpt-test',
      timing: { first_response_ms: 120, total_ms: 300 },
      usage: { prompt_tokens: 2, completion_tokens: 3, total_tokens: 5, usage_source: 'upstream' },
      billing: { usd: 0.001, cost_source: 'catalog' },
      success: true
    })
    showSuccess.mockReset()
  })

  it('loads a channel and records timing, usage, and response from a hello probe', async () => {
    const wrapper = mount(ChannelTestView, {
      global: { stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true } }
    })
    await flushPromises()

    expect(listAccounts).toHaveBeenCalledWith(1, 100, { lite: '1' })
    expect(getAvailableModels).toHaveBeenCalledWith(7)
    await wrapper.get('[data-testid="channel-test-model"]').setValue('gpt-test')
    await wrapper.get('[data-testid="channel-test-start"]').trigger('click')
    await flushPromises()

    expect(debugTestAccount).toHaveBeenCalledWith(
      7,
      { model_id: 'gpt-test', prompt: 'hello' },
      { signal: expect.any(AbortSignal) }
    )
    expect(wrapper.text()).toContain('hello back')
    expect(wrapper.text()).toContain('120 ms')
    expect(wrapper.text()).toContain('5')
    expect(JSON.parse(localStorage.getItem('sub2api.admin.channel-test.history') || '[]')).toHaveLength(1)
    wrapper.unmount()
  })
})
