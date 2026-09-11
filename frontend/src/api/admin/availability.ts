/**
 * Administrator availability testing API.
 *
 * This module intentionally follows the V2 contract instead of the legacy
 * debug-test endpoints. The Axios client unwraps the standard JSON envelope;
 * the stream is read with fetch because EventSource cannot send a POST body or
 * the admin Authorization header used by this application.
 */

import { apiClient, buildApiUrl } from '../client'
import { ADMIN_UI_REQUEST_HEADER } from '../adminUIRequest'
import { getLocale } from '@/i18n'

export type UpstreamSupport = 'declared' | 'unknown' | 'unsupported'
export type AvailabilityStatus = 'running' | 'succeeded' | 'failed' | 'cancelled' | string
export type TurnStatus = 'running' | 'succeeded' | 'failed' | 'cancelled'
export type AvailabilityEventType =
  | 'turn_started'
  | 'routing'
  | 'attempt_started'
  | 'attempt_failed'
  | 'content_delta'
  | 'turn_completed'
  | 'turn_failed'
  | 'turn_cancelled'

export interface AvailabilityAccount {
  id: number
  name: string
  platform: string
  status: string
  priority: number
  load_factor: number | null
  concurrency: number
  eligible: boolean
  reason?: string
  rank: number
  current_concurrency?: number | null
  queue_depth?: number | null
}

export interface AvailabilityLastTest {
  at: string
  account_id: number
  account_name: string
  model: string
  success: boolean
  first_response_ms: number | null
  total_ms: number | null
}

export interface AvailabilityModel {
  id: string
  upstream_model?: string
  upstream_support: UpstreamSupport
  downstream_allowed: boolean | null
  source: string
  account_ids: number[]
  mapping_effective: boolean
  last_test?: AvailabilityLastTest | null
}

export interface AvailabilityCatalog {
  accounts: AvailabilityAccount[]
  models: AvailabilityModel[]
  scheduling_note: string
}

export interface AvailabilityConversation {
  id: number
  group_id: number
  account_id: number | null
  model: string
  title: string
  created_at: string
  updated_at: string
}

export interface AvailabilityAttempt {
  id: number | string
  index: number
  account_id: number
  account_name: string
  requested_model: string
  model: string
  endpoint: string
  status: AvailabilityStatus
  started_at: string
  completed_at: string | null
  first_response_ms: number | null
  total_ms: number | null
  status_code?: number
  error?: string
  reason?: string
}

export interface AvailabilityTurn {
  id: number | string
  conversation_id: number
  prompt: string
  content: string
  status: TurnStatus
  created_at: string
  completed_at: string | null
  first_response_ms: number | null
  total_ms: number | null
  attempts: AvailabilityAttempt[]
  error?: string
}

export interface AvailabilityConversationPage {
  items: AvailabilityConversation[]
  total: number
}

export interface AvailabilityConversationDetail {
  conversation: AvailabilityConversation
  turns: AvailabilityTurn[]
  total?: number
  page?: number
  page_size?: number
}

export interface AvailabilityEvent {
  type: AvailabilityEventType
  conversation_id: number
  turn_id: number | string
  seq: number
  at: string
  attempt?: AvailabilityAttempt
  delta?: string
  turn?: AvailabilityTurn
  error?: string
}

export interface ListConversationsParams {
  page?: number
  page_size?: number
  /** Supported by the V2 history endpoint for server-side filtering. */
  search?: string
}

export interface StreamTurnPayload {
  prompt: string
  client_request_id: string
}

export interface StreamTurnOptions {
  signal?: AbortSignal
  onEvent?: (event: AvailabilityEvent) => void
}

export interface StreamTurnResult {
  receivedTerminalEvent: boolean
  lastSeq: number | null
}

const BASE_PATH = '/admin/channel-test'

export async function getCatalog(
  groupId: number,
  accountId?: number | null,
  options?: { signal?: AbortSignal }
): Promise<AvailabilityCatalog> {
  const { data } = await apiClient.get<AvailabilityCatalog>(`${BASE_PATH}/catalog`, {
    params: {
      group_id: groupId,
      ...(accountId != null ? { account_id: accountId } : {})
    },
    signal: options?.signal
  })
  return data
}

export async function createConversation(payload: {
  group_id: number
  account_id?: number
  model: string
  title?: string
}): Promise<AvailabilityConversation> {
  const { data } = await apiClient.post<AvailabilityConversation>(`${BASE_PATH}/conversations`, payload)
  return data
}

export async function listConversations(
  params: ListConversationsParams = {},
  options?: { signal?: AbortSignal }
): Promise<AvailabilityConversationPage> {
  const { data } = await apiClient.get<AvailabilityConversationPage>(`${BASE_PATH}/conversations`, {
    params: {
      page: params.page ?? 1,
      page_size: params.page_size ?? 20,
      ...(params.search?.trim() ? { search: params.search.trim() } : {})
    },
    signal: options?.signal
  })
  return data
}

export async function getConversation(
  id: number,
  params: { page?: number; page_size?: number } = {},
  options?: { signal?: AbortSignal }
): Promise<AvailabilityConversationDetail> {
  const { data } = await apiClient.get<AvailabilityConversationDetail>(`${BASE_PATH}/conversations/${id}`, {
    params: {
      page: params.page ?? 1,
      page_size: params.page_size ?? 50
    },
    signal: options?.signal
  })
  return data
}

function streamHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    Accept: 'text/event-stream',
    'Content-Type': 'application/json',
    'Accept-Language': getLocale(),
    [ADMIN_UI_REQUEST_HEADER]: '1'
  }
  const token = globalThis.localStorage?.getItem('auth_token')
  if (token) headers.Authorization = `Bearer ${token}`
  return headers
}

async function responseError(response: Response): Promise<Error> {
  let message = response.statusText || `HTTP ${response.status}`
  try {
    const body = await response.json() as {
      message?: string
      error?: string | { message?: string }
      data?: { message?: string }
    }
    if (typeof body.message === 'string' && body.message.trim()) message = body.message
    else if (typeof body.error === 'string' && body.error.trim()) message = body.error
    else if (body.error && typeof body.error !== 'string' && typeof body.error.message === 'string' && body.error.message.trim()) message = body.error.message
    else if (typeof body.data?.message === 'string' && body.data.message.trim()) message = body.data.message
  } catch {
    // Keep the HTTP status when the server did not return JSON.
  }
  const error = new Error(message)
  Object.assign(error, { status: response.status })
  return error
}

function isTerminalType(type: string): boolean {
  return type === 'turn_completed' || type === 'turn_failed' || type === 'turn_cancelled'
}

/** Consume JSON-in-data SSE frames while preserving the contract's sequence order. */
async function consumeSSE(
  body: ReadableStream<Uint8Array>,
  onEvent: (event: AvailabilityEvent) => void,
  signal?: AbortSignal
): Promise<StreamTurnResult> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let dataLines: string[] = []
  let receivedTerminalEvent = false
  let lastSeq: number | null = null

  const dispatch = () => {
    if (!dataLines.length) return
    const payload = dataLines.join('\n').trim()
    dataLines = []
    if (!payload) return
    const event = JSON.parse(payload) as AvailabilityEvent
    if (typeof event.seq === 'number') lastSeq = event.seq
    if (isTerminalType(event.type)) receivedTerminalEvent = true
    onEvent(event)
  }

  const processLine = (line: string) => {
    if (line === '') {
      dispatch()
      return
    }
    if (line.startsWith(':')) return
    if (line.startsWith('data:')) {
      dataLines.push(line.slice('data:'.length).replace(/^ /, ''))
    }
  }

  while (true) {
    if (signal?.aborted) throw new DOMException('The stream was aborted.', 'AbortError')
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split(/\r?\n/)
    buffer = lines.pop() || ''
    for (const line of lines) processLine(line)
  }

  buffer += decoder.decode()
  if (buffer) processLine(buffer)
  dispatch()
  return { receivedTerminalEvent, lastSeq }
}

export async function streamTurn(
  conversationId: number,
  payload: StreamTurnPayload,
  options: StreamTurnOptions = {}
): Promise<StreamTurnResult> {
  const response = await fetch(buildApiUrl(`${BASE_PATH}/conversations/${conversationId}/turns/stream`), {
    method: 'POST',
    headers: streamHeaders(),
    credentials: 'include',
    body: JSON.stringify(payload),
    signal: options.signal
  })
  if (!response.ok) throw await responseError(response)
  if (!response.body) return { receivedTerminalEvent: false, lastSeq: null }
  return consumeSSE(response.body, event => options.onEvent?.(event), options.signal)
}

export const availabilityAPI = {
  getCatalog,
  createConversation,
  listConversations,
  getConversation,
  streamTurn
}

export default availabilityAPI
