<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.channelTest.title')"
    width="extra-wide"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div
        v-if="account"
        class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 bg-gray-50 p-3 dark:border-dark-500 dark:bg-dark-700"
      >
        <div class="min-w-0">
          <div class="truncate font-semibold text-gray-900 dark:text-gray-100">{{ account.name }}</div>
          <div class="mt-1 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="rounded bg-gray-200 px-1.5 py-0.5 font-medium uppercase dark:bg-dark-500">{{ account.type }}</span>
            <span>{{ account.platform }}</span>
          </div>
        </div>
        <div class="text-right text-xs text-gray-500 dark:text-gray-400">
          <div>{{ t('admin.accounts.channelTest.modelCount', { count: models.length }) }}</div>
          <div v-if="batchTesting" class="mt-1 text-primary-600 dark:text-primary-400">
            {{ t('admin.accounts.channelTest.progress', batchProgress) }}
          </div>
        </div>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-2">
          <button
            v-if="batchTesting"
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="batchStopRequested"
            @click="stopBatchTest"
          >
            {{ batchStopRequested ? t('admin.accounts.channelTest.stopping') : t('admin.accounts.channelTest.stop') }}
          </button>
          <template v-else>
            <button
              type="button"
              class="btn btn-primary btn-sm"
              :disabled="loadingModels || models.length === 0 || testingModels.size > 0"
              @click="startBatchTest(filteredModels.map(model => model.id))"
            >
              {{ searchQuery.trim() ? t('admin.accounts.channelTest.testMatching', { count: filteredModels.length }) : t('admin.accounts.channelTest.testAll', { count: models.length }) }}
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="selectedModelIds.size === 0 || testingModels.size > 0"
              @click="startBatchTest([...selectedModelIds])"
            >
              {{ t('admin.accounts.channelTest.testSelected', { count: selectedModelIds.size }) }}
            </button>
          </template>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="loadingModels || batchTesting"
            @click="loadModels"
          >
            {{ t('admin.accounts.channelTest.refreshModels') }}
          </button>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="batchTesting || Object.keys(results).length === 0"
            @click="clearResults"
          >
            {{ t('admin.accounts.channelTest.clearResults') }}
          </button>
        </div>
        <label class="flex min-w-[220px] items-center gap-2">
          <span class="sr-only">{{ t('admin.accounts.channelTest.filterModels') }}</span>
          <input
            v-model="searchQuery"
            type="search"
            class="input input-sm w-full"
            :placeholder="t('admin.accounts.channelTest.filterModels')"
            :disabled="batchTesting"
          />
        </label>
      </div>

      <p v-if="batchNotice" class="rounded-lg bg-blue-50 px-3 py-2 text-xs text-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
        {{ batchNotice }}
      </p>
      <p class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        {{ t('admin.accounts.channelTest.costHint') }}
      </p>

      <div v-if="loadingModels" class="flex min-h-32 items-center justify-center rounded-xl border border-gray-200 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
        {{ t('common.loading') }}...
      </div>
      <div v-else-if="loadError" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300">
        {{ loadError }}
      </div>
      <div v-else-if="models.length === 0" class="flex min-h-32 items-center justify-center rounded-xl border border-dashed border-gray-300 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
        {{ t('admin.accounts.channelTest.noModels') }}
      </div>
      <div v-else class="max-h-[52vh] overflow-auto rounded-xl border border-gray-200 dark:border-dark-600">
        <table class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-600">
          <thead class="sticky top-0 z-10 bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="w-10 px-3 py-2 text-left">
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  :checked="allFilteredSelected"
                  :indeterminate="someFilteredSelected && !allFilteredSelected"
                  :disabled="filteredModels.length === 0 || batchTesting"
                  :aria-label="t('admin.accounts.channelTest.selectAll')"
                  @change="toggleFilteredSelection"
                />
              </th>
              <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.channelTest.model') }}</th>
              <th class="w-32 px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.channelTest.status') }}</th>
              <th class="px-3 py-2 text-left font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.channelTest.result') }}</th>
              <th class="w-24 px-3 py-2 text-right font-medium text-gray-600 dark:text-gray-300">{{ t('admin.accounts.channelTest.actions') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-800">
            <tr v-for="model in filteredModels" :key="model.id" class="hover:bg-gray-50 dark:hover:bg-dark-700/60">
              <td class="px-3 py-2 align-top">
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                  :checked="selectedModelIds.has(model.id)"
                  :disabled="batchTesting || isModelTesting(model.id)"
                  :aria-label="t('admin.accounts.channelTest.selectModel', { model: model.id })"
                  @change="toggleModelSelection(model.id)"
                />
              </td>
              <td class="max-w-[280px] px-3 py-2 align-top">
                <span class="block truncate font-medium text-gray-900 dark:text-gray-100" :title="model.id">
                  {{ model.display_name || model.id }}
                </span>
                <span v-if="model.display_name && model.display_name !== model.id" class="mt-0.5 block truncate font-mono text-xs text-gray-500 dark:text-gray-400" :title="model.id">
                  {{ model.id }}
                </span>
              </td>
              <td class="px-3 py-2 align-top">
                <span :class="statusClass(resultFor(model.id)?.status)">
                  {{ statusLabel(resultFor(model.id)?.status) }}
                </span>
              </td>
              <td class="max-w-[420px] px-3 py-2 align-top text-xs">
                <div v-if="resultFor(model.id)?.preview" class="whitespace-pre-wrap break-words text-gray-700 dark:text-gray-300">
                  {{ resultFor(model.id)?.preview }}
                </div>
                <div v-if="resultFor(model.id)?.error" class="mt-1 whitespace-pre-wrap break-words text-red-600 dark:text-red-400">
                  {{ resultFor(model.id)?.error }}
                </div>
                <span v-if="resultFor(model.id)?.responseTimeMs != null" class="mt-1 block text-gray-400 dark:text-gray-500">
                  {{ t('admin.accounts.channelTest.responseTime', { ms: resultFor(model.id)?.responseTimeMs }) }}
                </span>
                <span v-if="!resultFor(model.id)" class="text-gray-400 dark:text-gray-500">-</span>
              </td>
              <td class="px-3 py-2 text-right align-top">
                <button
                  type="button"
                  class="rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                  :disabled="batchTesting || isModelTesting(model.id)"
                  :aria-label="t('admin.accounts.channelTest.testModel', { model: model.id })"
                  :title="t('admin.accounts.channelTest.testModel', { model: model.id })"
                  @click="testModel(model.id)"
                >
                  <Icon v-if="isModelTesting(model.id)" name="refresh" size="sm" class="animate-spin" />
                  <Icon v-else name="play" size="sm" />
                </button>
              </td>
            </tr>
            <tr v-if="filteredModels.length === 0">
              <td colspan="5" class="px-3 py-8 text-center text-sm text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.channelTest.noMatchingModels') }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button
          type="button"
          class="rounded-lg bg-gray-100 px-4 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-300 dark:hover:bg-dark-500"
          @click="handleClose"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import BaseDialog from '@/components/common/BaseDialog.vue'
import { Icon } from '@/components/icons'
import { adminAPI } from '@/api/admin'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'
import { buildApiUrl } from '@/api/client'
import type { Account, ClaudeModel } from '@/types'

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

type ModelTestStatus = 'idle' | 'testing' | 'success' | 'error'

interface ModelTestResult {
  status: ModelTestStatus
  preview?: string
  error?: string
  responseTimeMs?: number
}

interface TestEvent {
  type?: string
  text?: string
  model?: string
  success?: boolean
  error?: string
  status?: string
}

const MAX_BATCH_CONCURRENCY = 3

const models = ref<ClaudeModel[]>([])
const selectedModelIds = ref<Set<string>>(new Set())
const results = ref<Record<string, ModelTestResult>>({})
const testingModels = ref<Set<string>>(new Set())
const loadingModels = ref(false)
const loadError = ref('')
const searchQuery = ref('')
const batchTesting = ref(false)
const batchStopRequested = ref(false)
const batchProgress = ref({ completed: 0, total: 0, success: 0, failed: 0 })
const batchNotice = ref('')
const controllers = new Map<string, AbortController>()
let modelsController: AbortController | null = null
let stopRequested = false
// A closed/reopened dialog must not allow an old, aborted batch to write into
// the new dialog state. The generation also covers switching to another row.
let viewGeneration = 0
let activeBatchGeneration = 0

const filteredModels = computed(() => {
  const keyword = searchQuery.value.trim().toLowerCase()
  if (!keyword) return models.value
  return models.value.filter((model) => {
    const id = model.id.toLowerCase()
    const displayName = (model.display_name || '').toLowerCase()
    return id.includes(keyword) || displayName.includes(keyword)
  })
})

const allFilteredSelected = computed(() => {
  return filteredModels.value.length > 0 && filteredModels.value.every((model) => selectedModelIds.value.has(model.id))
})

const someFilteredSelected = computed(() => {
  return filteredModels.value.some((model) => selectedModelIds.value.has(model.id))
})

// Image/video generation models require a different request shape and may
// incur a much higher upstream charge. They remain available for explicit
// single-model tests, but are excluded from "test all"/"test selected".
const isMediaModel = (modelID: string) => {
  const normalized = modelID.trim().toLowerCase()
  return normalized.startsWith('gpt-image-') ||
    normalized.startsWith('sora-') ||
    normalized.startsWith('sora_') ||
    /^gemini-[^/]*image/.test(normalized) ||
    normalized.startsWith('grok-imagine-image') ||
    normalized.startsWith('grok-imagine-video') ||
    normalized.startsWith('grok-video')
}

const resultFor = (modelID: string) => results.value[modelID]
const isModelTesting = (modelID: string) => testingModels.value.has(modelID)

const statusLabel = (status?: ModelTestStatus) => {
  switch (status) {
    case 'testing': return t('admin.accounts.channelTest.testing')
    case 'success': return t('admin.accounts.channelTest.success')
    case 'error': return t('admin.accounts.channelTest.failed')
    default: return t('admin.accounts.channelTest.pending')
  }
}

const statusClass = (status?: ModelTestStatus) => {
  const base = 'inline-flex rounded-full px-2 py-0.5 text-xs font-medium'
  switch (status) {
    case 'testing': return `${base} bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300`
    case 'success': return `${base} bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300`
    case 'error': return `${base} bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300`
    default: return `${base} bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300`
  }
}

const updateResult = (modelID: string, patch: Partial<ModelTestResult>) => {
  results.value = {
    ...results.value,
    [modelID]: {
      ...(results.value[modelID] || { status: 'idle' }),
      ...patch
    }
  }
}

const setModelTesting = (modelID: string, testing: boolean) => {
  const next = new Set(testingModels.value)
  if (testing) next.add(modelID)
  else next.delete(modelID)
  testingModels.value = next
}

const abortAllTests = () => {
  modelsController?.abort()
  modelsController = null
  const abortedModelIDs = [...controllers.keys()]
  for (const controller of controllers.values()) controller.abort()
  controllers.clear()
  if (abortedModelIDs.length > 0) {
    const next = new Set(testingModels.value)
    abortedModelIDs.forEach((modelID) => next.delete(modelID))
    testingModels.value = next
  }
}

const resetState = () => {
  viewGeneration += 1
  activeBatchGeneration = 0
  stopRequested = true
  abortAllTests()
  models.value = []
  selectedModelIds.value = new Set()
  results.value = {}
  testingModels.value = new Set()
  loadingModels.value = false
  loadError.value = ''
  searchQuery.value = ''
  batchTesting.value = false
  batchStopRequested.value = false
  batchProgress.value = { completed: 0, total: 0, success: 0, failed: 0 }
  batchNotice.value = ''
}

const loadModels = async () => {
  if (!props.account) return
  const accountID = props.account.id
  const generation = viewGeneration
  modelsController?.abort()
  const controller = new AbortController()
  modelsController = controller
  loadingModels.value = true
  loadError.value = ''
  try {
    const available = await adminAPI.accounts.getAvailableModels(accountID, { signal: controller.signal })
    if (generation !== viewGeneration || !props.show || !props.account || props.account.id !== accountID) return
    const seen = new Set<string>()
    models.value = available
      .filter((model) => model && typeof model.id === 'string' && model.id.trim())
      .filter((model) => {
        if (seen.has(model.id)) return false
        seen.add(model.id)
        return true
      })
      .sort((a, b) => (a.display_name || a.id).localeCompare(b.display_name || b.id))
    selectedModelIds.value = new Set()
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return
    if (generation !== viewGeneration || !props.show || !props.account || props.account.id !== accountID) return
    loadError.value = error instanceof Error ? error.message : t('admin.accounts.channelTest.loadFailed')
    models.value = []
  } finally {
    if (modelsController === controller) {
      modelsController = null
      if (generation === viewGeneration && props.account?.id === accountID) loadingModels.value = false
    }
  }
}

const parseSSE = async (response: Response, onEvent: (event: TestEvent) => void) => {
  const reader = response.body?.getReader()
  if (!reader) throw new Error(t('admin.accounts.channelTest.noResponseBody'))
  const decoder = new TextDecoder()
  let buffer = ''

  const processLine = (line: string) => {
    const trimmed = line.trim()
    if (!trimmed.startsWith('data:')) return
    const payload = trimmed.slice(5).trim()
    if (!payload || payload === '[DONE]') return
    try {
      const event = JSON.parse(payload) as TestEvent
      onEvent(event)
    } catch {
      // Ignore keep-alive or malformed non-event lines; a missing completion
      // event is reported as a failed test after the stream ends.
    }
  }

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split(/\r?\n/)
    buffer = lines.pop() || ''
    for (const line of lines) processLine(line)
  }
  buffer += decoder.decode()
  if (buffer) processLine(buffer)
}

const getResponseError = async (response: Response) => {
  const text = (await response.text()).trim()
  if (!text) return `HTTP ${response.status}`
  try {
    const payload = JSON.parse(text) as { message?: string; error?: string }
    return payload.message || payload.error || `HTTP ${response.status}`
  } catch {
    return text.slice(0, 300) || `HTTP ${response.status}`
  }
}

const runModelTest = async (modelID: string, generation = viewGeneration): Promise<ModelTestResult> => {
  if (!props.account) return { status: 'error', error: t('admin.accounts.channelTest.noAccount') }
  if (generation !== viewGeneration) return { status: 'idle' }
  const startedAt = Date.now()
  const controller = new AbortController()
  controllers.set(modelID, controller)
  setModelTesting(modelID, true)
  if (generation === viewGeneration) {
    updateResult(modelID, { status: 'testing', preview: '', error: undefined, responseTimeMs: undefined })
  }

  let completed = false
  let succeeded = false
  let streamError = ''
  let preview = ''

  try {
    const response = await fetch(buildApiUrl(`/admin/accounts/${props.account.id}/test`), {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${localStorage.getItem('auth_token') || ''}`,
        'Content-Type': 'application/json',
        Accept: 'text/event-stream',
        [ADMIN_UI_REQUEST_HEADER]: '1'
      },
      body: JSON.stringify({ model_id: modelID, prompt: '' }),
      signal: controller.signal
    })
    if (!response.ok) throw new Error(await getResponseError(response))

    await parseSSE(response, (event) => {
      if (event.type === 'content' && event.text) {
        preview = `${preview}${event.text}`.slice(0, 240)
        if (generation === viewGeneration) updateResult(modelID, { preview })
      } else if (event.type === 'status' && event.text && !preview) {
        if (generation === viewGeneration) updateResult(modelID, { preview: event.text.slice(0, 240) })
      } else if (event.type === 'test_start' && event.model) {
        if (generation === viewGeneration) {
          updateResult(modelID, { preview: t('admin.accounts.channelTest.usingModel', { model: event.model }) })
        }
      } else if (event.type === 'image' || event.type === 'video' || event.type === 'audio') {
        preview = t('admin.accounts.channelTest.mediaReceived')
        if (generation === viewGeneration) updateResult(modelID, { preview })
      } else if (event.type === 'error') {
        streamError = event.error || t('admin.accounts.channelTest.failed')
      } else if (event.type === 'test_complete') {
        completed = true
        succeeded = event.success === true
        if (!succeeded) streamError = event.error || t('admin.accounts.channelTest.failed')
      }
    })

    if (controller.signal.aborted) throw new DOMException('Aborted', 'AbortError')
    if (!completed) throw new Error(streamError || t('admin.accounts.channelTest.incomplete'))

    const finalResult: ModelTestResult = {
      status: succeeded ? 'success' : 'error',
      preview: preview || (succeeded ? t('admin.accounts.channelTest.success') : undefined),
      error: succeeded ? undefined : streamError || t('admin.accounts.channelTest.failed'),
      responseTimeMs: Date.now() - startedAt
    }
    if (generation === viewGeneration) updateResult(modelID, finalResult)
    return finalResult
  } catch (error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      const cancelled: ModelTestResult = { status: 'idle', preview: '', error: stopRequested ? t('admin.accounts.channelTest.cancelled') : undefined }
      if (generation === viewGeneration) updateResult(modelID, cancelled)
      return cancelled
    }
    const failed: ModelTestResult = {
      status: 'error',
      error: error instanceof Error ? error.message : t('admin.accounts.channelTest.failed'),
      responseTimeMs: Date.now() - startedAt
    }
    if (generation === viewGeneration) updateResult(modelID, failed)
    return failed
  } finally {
    // An aborted request can finish after a newer request for the same model
    // has started. Only the request that still owns the map entry may clear
    // the controller/testing state for that model.
    if (controllers.get(modelID) === controller) {
      controllers.delete(modelID)
      if (generation === viewGeneration) setModelTesting(modelID, false)
    }
  }
}

const testModel = async (modelID: string) => {
  if (batchTesting.value || isModelTesting(modelID)) return
  stopRequested = false
  await runModelTest(modelID, viewGeneration)
}

const startBatchTest = async (modelIDs: string[]) => {
  const uniqueModels = [...new Set(modelIDs.map((id) => id.trim()).filter(Boolean))]
  if (batchTesting.value || uniqueModels.length === 0) return

  const testableModels = uniqueModels.filter((modelID) => !isMediaModel(modelID))
  const skippedMediaCount = uniqueModels.length - testableModels.length
  batchNotice.value = skippedMediaCount > 0
    ? t('admin.accounts.channelTest.batchSkippedMedia', { count: skippedMediaCount })
    : ''
  if (testableModels.length === 0) return

  stopRequested = false
  const runGeneration = viewGeneration
  activeBatchGeneration = runGeneration
  batchTesting.value = true
  batchStopRequested.value = false
  batchProgress.value = { completed: 0, total: testableModels.length, success: 0, failed: 0 }
  let nextIndex = 0

  const worker = async () => {
    while (!stopRequested && activeBatchGeneration === runGeneration && viewGeneration === runGeneration) {
      const index = nextIndex++
      if (index >= testableModels.length) return
      const result = await runModelTest(testableModels[index], runGeneration)
      if (viewGeneration !== runGeneration) return
      batchProgress.value = {
        ...batchProgress.value,
        completed: batchProgress.value.completed + 1,
        success: batchProgress.value.success + (result.status === 'success' ? 1 : 0),
        failed: batchProgress.value.failed + (result.status === 'error' ? 1 : 0)
      }
    }
  }

  try {
    await Promise.all(Array.from({ length: Math.min(MAX_BATCH_CONCURRENCY, testableModels.length) }, () => worker()))
  } finally {
    if (viewGeneration === runGeneration) {
      batchTesting.value = false
      batchStopRequested.value = false
      stopRequested = false
      activeBatchGeneration = 0
    }
  }
}

const stopBatchTest = () => {
  if (!batchTesting.value) return
  stopRequested = true
  batchStopRequested.value = true
  abortAllTests()
}

const toggleModelSelection = (modelID: string) => {
  const next = new Set(selectedModelIds.value)
  if (next.has(modelID)) next.delete(modelID)
  else next.add(modelID)
  selectedModelIds.value = next
}

const toggleFilteredSelection = (event: Event) => {
  const checked = (event.target as HTMLInputElement).checked
  const next = new Set(selectedModelIds.value)
  for (const model of filteredModels.value) {
    if (checked) next.add(model.id)
    else next.delete(model.id)
  }
  selectedModelIds.value = next
}

const clearResults = () => {
  results.value = {}
}

const handleClose = () => {
  stopRequested = true
  activeBatchGeneration = 0
  viewGeneration += 1
  abortAllTests()
  emit('close')
}

watch(
  () => [props.show, props.account?.id] as const,
  async ([show]) => {
    if (show && props.account) {
      resetState()
      stopRequested = false
      await loadModels()
    } else {
      stopRequested = true
      abortAllTests()
    }
  }
)

onUnmounted(() => {
  stopRequested = true
  activeBatchGeneration = 0
  viewGeneration += 1
  abortAllTests()
})
</script>
