import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import ChannelTestView from '../ChannelTestView.vue'

const { getAll, getCatalog, createConversation, listConversations, getConversation, streamTurn, update, showError, showSuccess } = vi.hoisted(() => ({
  getAll: vi.fn(),
  getCatalog: vi.fn(),
  createConversation: vi.fn(),
  listConversations: vi.fn(),
  getConversation: vi.fn(),
  streamTurn: vi.fn(),
  update: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { getAll },
    accounts: { update },
    availability: {
      getCatalog,
      createConversation,
      listConversations,
      getConversation,
      streamTurn
    }
  }
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const group = {
  id: 12,
  name: 'main pool',
  platform: 'openai',
  status: 'active'
}

const catalog = {
  accounts: [
    {
      id: 7,
      name: 'first account',
      platform: 'openai',
      status: 'active',
      priority: 10,
      load_factor: 3,
      concurrency: 4,
      current_concurrency: 1,
      queue_depth: 0,
      eligible: true,
      rank: 1
    },
    {
      id: 8,
      name: 'second account',
      platform: 'openai',
      status: 'active',
      priority: 20,
      load_factor: null,
      concurrency: 2,
      current_concurrency: 2,
      queue_depth: 1,
      eligible: false,
      reason: 'rate limited',
      rank: 2
    }
  ],
  models: [
    {
      id: 'gpt-test',
      upstream_model: 'gpt-test-upstream',
      upstream_support: 'declared',
      downstream_allowed: true,
      source: 'group allowlist',
      account_ids: [7],
      mapping_effective: true,
      last_test: null
    },
    {
      id: 'closed-model',
      upstream_support: 'unknown',
      downstream_allowed: false,
      source: 'downstream policy',
      account_ids: [],
      mapping_effective: false,
      last_test: null
    }
  ],
  scheduling_note: 'server scheduler order'
}

const conversation = {
  id: 99,
  group_id: 12,
  account_id: null,
  model: 'gpt-test',
  title: 'hello',
  created_at: '2026-09-11T02:00:00Z',
  updated_at: '2026-09-11T02:00:00Z'
}

function makeTurn(status: 'running' | 'succeeded' | 'failed' | 'cancelled' = 'succeeded') {
  return {
    id: 101,
    conversation_id: 99,
    prompt: 'hello',
    content: status === 'succeeded' ? 'hello back' : '',
    status,
    created_at: '2026-09-11T02:00:00Z',
    completed_at: status === 'running' ? null : '2026-09-11T02:00:00.300Z',
    first_response_ms: status === 'succeeded' ? 120 : null,
    total_ms: status === 'succeeded' ? 300 : null,
    attempts: [
      {
        id: 'attempt-1',
        index: 1,
        account_id: 8,
        account_name: 'second account',
        requested_model: 'gpt-test',
        model: 'gpt-test-upstream',
        endpoint: '/v1/responses',
        status: 'failed',
        started_at: '2026-09-11T02:00:00Z',
        completed_at: '2026-09-11T02:00:00.040Z',
        first_response_ms: null,
        total_ms: 40,
        reason: 'rate limited'
      },
      {
        id: 'attempt-2',
        index: 2,
        account_id: 7,
        account_name: 'first account',
        requested_model: 'gpt-test',
        model: 'gpt-test-upstream',
        endpoint: '/v1/responses',
        status: status === 'running' ? 'running' : 'succeeded',
        started_at: '2026-09-11T02:00:00.050Z',
        completed_at: status === 'running' ? null : '2026-09-11T02:00:00.300Z',
        first_response_ms: status === 'running' ? null : 120,
        total_ms: status === 'running' ? null : 250
      }
    ]
  }
}

describe('Availability V2 administrator page', () => {
  beforeEach(() => {
    localStorage.clear()
    getAll.mockReset().mockResolvedValue([group])
    getCatalog.mockReset().mockResolvedValue(catalog)
    createConversation.mockReset().mockResolvedValue(conversation)
    listConversations.mockReset().mockResolvedValue({ items: [], total: 0 })
    getConversation.mockReset().mockResolvedValue({ conversation, turns: [makeTurn()], total: 1, page: 1, page_size: 50 })
    update.mockReset().mockResolvedValue({ id: 7, priority: 10, load_factor: 3 })
    streamTurn.mockReset().mockImplementation(async (_id, _payload, options) => {
      const runningTurn = makeTurn('running')
      const completedTurn = makeTurn()
      options.onEvent({ type: 'turn_started', conversation_id: 99, turn_id: 101, seq: 1, at: '2026-09-11T02:00:00Z', turn: runningTurn })
      options.onEvent({ type: 'attempt_started', conversation_id: 99, turn_id: 101, seq: 2, at: '2026-09-11T02:00:00Z', attempt: runningTurn.attempts[1] })
      options.onEvent({ type: 'attempt_failed', conversation_id: 99, turn_id: 101, seq: 3, at: '2026-09-11T02:00:00Z', attempt: completedTurn.attempts[0], error: 'rate limited' })
      options.onEvent({ type: 'attempt_started', conversation_id: 99, turn_id: 101, seq: 4, at: '2026-09-11T02:00:00Z', attempt: completedTurn.attempts[1] })
      options.onEvent({ type: 'content_delta', conversation_id: 99, turn_id: 101, seq: 5, at: '2026-09-11T02:00:00Z', delta: 'hello back' })
      options.onEvent({ type: 'turn_completed', conversation_id: 99, turn_id: 101, seq: 6, at: '2026-09-11T02:00:00Z', turn: completedTurn })
      return { receivedTerminalEvent: true, lastSeq: 6 }
    })
    showError.mockReset()
    showSuccess.mockReset()
  })

  function mountPage() {
    return mount(ChannelTestView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          Icon: true
        }
      }
    })
  }

  it('loads only the selected group catalog and shows server scheduler fields', async () => {
    const wrapper = mountPage()
    await flushPromises()

    expect(getCatalog).toHaveBeenCalledWith(12, null, { signal: expect.any(AbortSignal) })
    expect(wrapper.text()).toContain('first account')
    expect(wrapper.text()).toContain('second account')
    expect(wrapper.text()).toContain('server scheduler order')
    expect(wrapper.text()).toContain('10')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.find('[data-testid="availability-account-7"]').exists()).toBe(true)
    expect(localStorage.getItem('sub2api.admin.channel-test.history')).toBeNull()
    wrapper.unmount()
  })

  it('creates an automatic durable conversation and renders every routed attempt', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="availability-model-gpt-test"]').trigger('click')
    await wrapper.get('[data-testid="availability-prompt"]').setValue('hello')
    await wrapper.get('[data-testid="availability-send"]').trigger('submit')
    await flushPromises()

    expect(createConversation).toHaveBeenCalledWith({ group_id: 12, model: 'gpt-test', title: 'hello' })
    expect(streamTurn).toHaveBeenCalledWith(
      99,
      { prompt: 'hello', client_request_id: expect.any(String) },
      { signal: expect.any(AbortSignal), onEvent: expect.any(Function) }
    )
    expect(wrapper.text()).toContain('hello back')
    expect(wrapper.text()).toContain('second account')
    expect(wrapper.text()).toContain('first account')
    expect(wrapper.text()).toContain('attemptFailedEvent')
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('pins a selected account and rolls an edit back when the existing update API fails', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="availability-account-7"]').trigger('click')
    await flushPromises()
    expect(getCatalog).toHaveBeenLastCalledWith(12, 7, { signal: expect.any(AbortSignal) })

    update.mockRejectedValueOnce(new Error('validation failed'))
    const input = wrapper.get('[data-testid="availability-account-7-priority"]')
    await input.setValue('42')
    await input.trigger('blur')
    await flushPromises()

    expect(update).toHaveBeenCalledWith(7, { priority: 42 })
    expect((input.element as HTMLInputElement).value).toBe('10')
    expect(wrapper.text()).toContain('validation failed')
    wrapper.unmount()
  })

  it('sends an explicit account id only after the operator pins that account', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="availability-account-7"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="availability-model-gpt-test"]').trigger('click')
    await wrapper.get('[data-testid="availability-prompt"]').setValue('pinned probe')
    await wrapper.get('[data-testid="availability-send"]').trigger('submit')
    await flushPromises()

    expect(createConversation).toHaveBeenCalledWith({
      group_id: 12,
      account_id: 7,
      model: 'gpt-test',
      title: 'pinned probe'
    })
    expect(streamTurn).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('refreshes persisted conversation state after a stream ends without a terminal event', async () => {
    streamTurn.mockResolvedValueOnce({ receivedTerminalEvent: false, lastSeq: 2 })
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('[data-testid="availability-model-gpt-test"]').trigger('click')
    await wrapper.get('[data-testid="availability-prompt"]').setValue('hello')
    await wrapper.get('[data-testid="availability-send"]').trigger('submit')
    await flushPromises()

    expect(getConversation).toHaveBeenCalledWith(99, { page: 1, page_size: 50 })
    expect(wrapper.text()).toContain('disconnected')
    const firstRequest = streamTurn.mock.calls[0][1] as { prompt: string; client_request_id: string }
    await wrapper.get('[data-testid="availability-reconnect"]').trigger('click')
    await flushPromises()

    expect(streamTurn).toHaveBeenCalledTimes(2)
    expect(streamTurn.mock.calls[1][1]).toEqual({ prompt: 'hello', client_request_id: firstRequest.client_request_id })
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })
})
