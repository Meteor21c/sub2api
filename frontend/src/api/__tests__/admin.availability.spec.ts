import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post },
  buildApiUrl: (path: string) => `/api/v1${path}`
}))

vi.mock('@/api/adminUIRequest', () => ({
  ADMIN_UI_REQUEST_HEADER: 'X-Admin-UI-Request'
}))

vi.mock('@/i18n', () => ({
  getLocale: () => 'zh'
}))

import {
  createConversation,
  getCatalog,
  listConversations,
  streamTurn
} from '@/api/admin/availability'

function frame(payload: Record<string, unknown>): string {
  return `data: ${JSON.stringify(payload)}\n\n`
}

function streamFrom(...chunks: string[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      const encoder = new TextEncoder()
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
      controller.close()
    }
  })
}

describe('availability V2 API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    localStorage.clear()
    localStorage.setItem('auth_token', 'admin-token')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('keeps catalog and conversation requests in the admin V2 namespace', async () => {
    get.mockResolvedValueOnce({ data: { accounts: [], models: [], scheduling_note: 'server' } })
    post.mockResolvedValueOnce({ data: { id: 4, group_id: 2, account_id: null, model: 'gpt-test', title: 'probe' } })

    await getCatalog(2)
    await createConversation({ group_id: 2, model: 'gpt-test', title: 'probe' })

    expect(get).toHaveBeenCalledWith('/admin/channel-test/catalog', {
      params: { group_id: 2 },
      signal: undefined
    })
    expect(post).toHaveBeenCalledWith('/admin/channel-test/conversations', {
      group_id: 2,
      model: 'gpt-test',
      title: 'probe'
    })
  })

  it('sends authenticated POST SSE with the exact client request id and parses terminal events', async () => {
    const events = [
      { type: 'turn_started', conversation_id: 4, turn_id: 8, seq: 1, at: '2026-09-11T02:00:00Z' },
      { type: 'attempt_started', conversation_id: 4, turn_id: 8, seq: 2, at: '2026-09-11T02:00:00Z' },
      { type: 'content_delta', conversation_id: 4, turn_id: 8, seq: 3, at: '2026-09-11T02:00:00Z', delta: 'ok' },
      { type: 'turn_completed', conversation_id: 4, turn_id: 8, seq: 4, at: '2026-09-11T02:00:00Z' }
    ]
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, body: streamFrom(`${frame(events[0])}${frame(events[1])}`, `${frame(events[2])}${frame(events[3])}`) })
    vi.stubGlobal('fetch', fetchMock)
    const received: unknown[] = []
    const controller = new AbortController()

    const result = await streamTurn(
      4,
      { prompt: 'probe', client_request_id: 'availability-request-1' },
      { signal: controller.signal, onEvent: event => received.push(event) }
    )

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/channel-test/conversations/4/turns/stream', expect.objectContaining({
      method: 'POST',
      credentials: 'include',
      signal: controller.signal,
      body: JSON.stringify({ prompt: 'probe', client_request_id: 'availability-request-1' }),
      headers: expect.objectContaining({
        Accept: 'text/event-stream',
        Authorization: 'Bearer admin-token',
        'X-Admin-UI-Request': '1',
        'Accept-Language': 'zh'
      })
    }))
    expect(received.map(event => (event as { type: string }).type)).toEqual([
      'turn_started',
      'attempt_started',
      'content_delta',
      'turn_completed'
    ])
    expect(result).toEqual({ receivedTerminalEvent: true, lastSeq: 4 })
  })

  it('passes server-side history search and pagination without local persistence', async () => {
    get.mockResolvedValueOnce({ data: { items: [], total: 0 } })
    await listConversations({ page: 3, page_size: 10, search: '  route  ' })

    expect(get).toHaveBeenCalledWith('/admin/channel-test/conversations', {
      params: { page: 3, page_size: 10, search: 'route' },
      signal: undefined
    })
    expect(localStorage.getItem('sub2api.admin.channel-test.history')).toBeNull()
  })
})
