<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
        <section class="card p-5 md:p-6">
          <div class="mb-5 flex items-start justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                {{ t('admin.channelTest.selectTitle') }}
              </h2>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ t('admin.channelTest.selectDescription') }}
              </p>
            </div>
            <button
              type="button"
              class="btn btn-secondary px-3"
              :disabled="loadingAccounts || loadingGroups || running"
              :title="t('common.refresh')"
              @click="refreshAll"
            >
              <Icon name="refresh" size="sm" :class="loadingAccounts || loadingGroups ? 'animate-spin' : ''" />
            </button>
          </div>

          <div class="space-y-4">
            <div class="grid grid-cols-2 gap-2" data-testid="availability-test-mode">
              <button type="button" class="btn" :class="testMode === 'account' ? 'btn-primary' : 'btn-secondary'" :disabled="running" @click="testMode = 'account'">
                {{ t('admin.channelTest.accountMode') }}
              </button>
              <button type="button" class="btn" :class="testMode === 'group' ? 'btn-primary' : 'btn-secondary'" :disabled="running" @click="testMode = 'group'">
                {{ t('admin.channelTest.groupMode') }}
              </button>
            </div>

            <label v-if="testMode === 'account'" class="block">
              <span class="input-label">{{ t('admin.channelTest.account') }}</span>
              <select v-model.number="selectedAccountId" data-testid="channel-test-account" class="input w-full" :disabled="loadingAccounts || running">
                <option :value="null">{{ t('admin.channelTest.selectAccount') }}</option>
                <option v-for="account in accounts" :key="account.id" :value="account.id">
                  {{ account.name }} · {{ account.platform }} · {{ account.type }}
                  <template v-if="account.status !== 'active'">（{{ account.status }}）</template>
                </option>
              </select>
            </label>

            <label v-else class="block">
              <span class="input-label">{{ t('admin.channelTest.group') }}</span>
              <select v-model.number="selectedGroupId" data-testid="channel-test-group" class="input w-full" :disabled="loadingGroups || running">
                <option :value="null">{{ t('admin.channelTest.selectGroup') }}</option>
                <option v-for="group in groups" :key="group.id" :value="group.id">
                  {{ group.name }} · {{ group.platform }}
                </option>
              </select>
            </label>

            <label class="block">
              <span class="input-label">{{ t('admin.channelTest.model') }}</span>
              <select v-model="selectedModelId" data-testid="channel-test-model" class="input w-full" :disabled="loadingModels || running || !selectedTargetId">
                <option value="">{{ t('admin.channelTest.autoModel') }}</option>
                <option v-for="model in modelOptions" :key="model.id" :value="model.id">
                  {{ model.label }}
                </option>
              </select>
              <span v-if="loadingModels" class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
                {{ t('common.loading') }}
              </span>
            </label>

            <label class="block">
              <span class="input-label">{{ t('admin.channelTest.prompt') }}</span>
              <textarea
                v-model="prompt"
                data-testid="channel-test-prompt"
                rows="4"
                class="input w-full resize-y"
                :placeholder="t('admin.channelTest.promptPlaceholder')"
                :disabled="running"
              />
            </label>

            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                data-testid="channel-test-start"
                class="btn btn-primary"
                :disabled="running || !selectedTargetId"
                @click="runTest"
              >
                <Icon v-if="running" name="refresh" size="sm" class="mr-1 animate-spin" />
                <Icon v-else name="play" size="sm" class="mr-1" />
                {{ running ? t('admin.channelTest.testing') : t('admin.channelTest.start') }}
              </button>
              <button v-if="running" type="button" class="btn btn-secondary" @click="stopTest">
                {{ t('admin.channelTest.stop') }}
              </button>
              <button
                type="button"
                class="btn btn-secondary"
                :disabled="loadingModels || running || !selectedTargetId"
                @click="loadModels"
              >
                {{ t('admin.channelTest.refreshModels') }}
              </button>
            </div>
          </div>
        </section>

        <section class="card p-5 md:p-6">
          <div class="mb-5 flex items-center justify-between gap-3">
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.channelTest.resultTitle') }}
            </h2>
            <span
              v-if="result"
              :class="result.success ? 'badge badge-success' : 'badge badge-danger'"
            >
              {{ result.success ? t('common.success') : t('common.error') }}
            </span>
          </div>

          <div v-if="errorMessage" class="mb-4 rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">
            {{ errorMessage }}
          </div>

          <div v-if="result" class="space-y-5">
            <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
                <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.firstResponse') }}</div>
                <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ formatMs(result.timing.first_response_ms) }}</div>
              </div>
              <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
                <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.totalTime') }}</div>
                <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ formatMs(result.timing.total_ms) }}</div>
              </div>
              <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
                <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.totalTokens') }}</div>
                <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ formatTokens(result.usage.total_tokens) }}</div>
              </div>
              <div class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800">
                <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.estimatedCost') }}</div>
                <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ formatCost(result.billing.usd, result.billing.cost_source) }}</div>
              </div>
            </div>

            <div class="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-4">
              <div v-if="result.account_name"><span class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.selectedAccount') }}</span><span class="ml-2 font-medium text-gray-900 dark:text-white">{{ result.account_name }}</span></div>
              <div><span class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.model') }}</span><span class="ml-2 font-medium text-gray-900 dark:text-white">{{ result.model || '—' }}</span></div>
              <div><span class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.inputTokens') }}</span><span class="ml-2 font-medium text-gray-900 dark:text-white">{{ formatTokens(result.usage.prompt_tokens) }}</span></div>
              <div><span class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.outputTokens') }}</span><span class="ml-2 font-medium text-gray-900 dark:text-white">{{ formatTokens(result.usage.completion_tokens) }}</span></div>
              <div><span class="text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.usageSource') }}</span><span class="ml-2 font-medium text-gray-900 dark:text-white">{{ usageSourceLabel(result.usage.usage_source) }}</span></div>
            </div>

            <div>
              <div class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.channelTest.response') }}</div>
              <pre class="max-h-72 overflow-auto whitespace-pre-wrap rounded-xl bg-gray-900 p-4 text-sm leading-6 text-gray-100">{{ result.content || t('admin.channelTest.emptyResponse') }}</pre>
            </div>
          </div>

          <div v-else class="flex min-h-64 items-center justify-center rounded-xl border border-dashed border-gray-300 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
            {{ running ? t('admin.channelTest.waiting') : t('admin.channelTest.noResult') }}
          </div>
        </section>
      </div>

      <section class="card overflow-hidden">
        <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-700">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.channelTest.historyTitle') }}</h2>
          <button v-if="history.length" type="button" class="text-sm text-gray-500 hover:text-red-500 dark:text-gray-400" @click="clearHistory">
            {{ t('admin.channelTest.clearHistory') }}
          </button>
        </div>
        <div v-if="history.length" class="overflow-x-auto">
          <table class="min-w-full text-left text-sm">
            <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr>
                <th class="px-5 py-3">{{ t('admin.channelTest.time') }}</th>
                <th class="px-5 py-3">{{ t('admin.channelTest.target') }}</th>
                <th class="px-5 py-3">{{ t('admin.channelTest.model') }}</th>
                <th class="px-5 py-3">{{ t('admin.channelTest.totalTime') }}</th>
                <th class="px-5 py-3">{{ t('common.status') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="entry in history" :key="entry.id" class="cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-800" @click="openHistory(entry)">
                <td class="whitespace-nowrap px-5 py-3 text-gray-500 dark:text-gray-400">{{ formatDate(entry.createdAt) }}</td>
                <td class="px-5 py-3 font-medium text-gray-900 dark:text-white">{{ entry.targetName || entry.accountName }}</td>
                <td class="px-5 py-3 text-gray-600 dark:text-gray-300">{{ entry.result?.model || entry.model || '—' }}</td>
                <td class="px-5 py-3 text-gray-600 dark:text-gray-300">{{ entry.result ? formatMs(entry.result.timing.total_ms) : '—' }}</td>
                <td class="px-5 py-3">
                  <span :class="entry.result?.success ? 'badge badge-success' : 'badge badge-danger'">
                    {{ entry.result?.success ? t('common.success') : t('common.error') }}
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="px-5 py-10 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.noHistory') }}</div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AccountDebugTestResult } from '@/api/admin/accounts'
import Icon from '@/components/icons/Icon.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import type { AccountListItem, AdminGroup, ClaudeModel } from '@/types'

interface ModelOption {
  id: string
  label: string
}

interface HistoryEntry {
  id: string
  createdAt: string
  accountId: number
  accountName: string
  targetName?: string
  targetType?: 'account' | 'group'
  model: string
  result: AccountDebugTestResult | null
  error?: string
}

const HISTORY_KEY = 'sub2api.admin.channel-test.history'
const MAX_HISTORY = 20

const { t } = useI18n()
const appStore = useAppStore()
const accounts = ref<AccountListItem[]>([])
const groups = ref<AdminGroup[]>([])
const testMode = ref<'account' | 'group'>('account')
const selectedAccountId = ref<number | null>(null)
const selectedGroupId = ref<number | null>(null)
const models = ref<ModelOption[]>([])
const selectedModelId = ref('')
const prompt = ref('hello')
const result = ref<AccountDebugTestResult | null>(null)
const errorMessage = ref('')
const history = ref<HistoryEntry[]>([])
const loadingAccounts = ref(false)
const loadingGroups = ref(false)
const loadingModels = ref(false)
const running = ref(false)
const testController = ref<AbortController | null>(null)
const modelLoadVersion = ref(0)

const selectedAccount = computed(() => accounts.value.find(account => account.id === selectedAccountId.value) ?? null)
const selectedGroup = computed(() => groups.value.find(group => group.id === selectedGroupId.value) ?? null)
const selectedTargetId = computed(() => testMode.value === 'account' ? selectedAccountId.value : selectedGroupId.value)
const modelOptions = computed(() => models.value)

function errorText(error: unknown): string {
  if (error && typeof error === 'object' && 'message' in error) {
    const message = (error as { message?: unknown }).message
    if (typeof message === 'string' && message.trim()) return message
  }
  return t('admin.channelTest.requestFailed')
}

async function loadAccounts() {
  loadingAccounts.value = true
  try {
    const loaded: AccountListItem[] = []
    let page = 1
    let pages = 1
    do {
      const response = await adminAPI.accounts.list(page, 100, { lite: '1' })
      loaded.push(...response.items)
      pages = response.pages || 1
      page += 1
    } while (page <= pages)
    accounts.value = loaded
    if (!selectedAccountId.value || !loaded.some(account => account.id === selectedAccountId.value)) {
      selectedAccountId.value = loaded[0]?.id ?? null
    }
  } catch (error) {
    errorMessage.value = errorText(error)
    appStore.showError(errorMessage.value)
  } finally {
    loadingAccounts.value = false
  }
}

async function loadGroups() {
  loadingGroups.value = true
  try {
    groups.value = await adminAPI.groups.getAll()
    if (!selectedGroupId.value || !groups.value.some(group => group.id === selectedGroupId.value)) {
      selectedGroupId.value = groups.value[0]?.id ?? null
    }
  } catch (error) {
    errorMessage.value = errorText(error)
    appStore.showError(errorMessage.value)
  } finally {
    loadingGroups.value = false
  }
}

async function loadModels() {
  const targetId = selectedTargetId.value
  if (!targetId) {
    models.value = []
    selectedModelId.value = ''
    return
  }
  const version = ++modelLoadVersion.value
  loadingModels.value = true
  selectedModelId.value = ''
  try {
    const available = testMode.value === 'account'
      ? await adminAPI.accounts.getAvailableModels(targetId)
      : (await adminAPI.groups.getModelAllowlistCandidates(targetId, selectedGroup.value?.platform)).map(id => ({ id, display_name: id }))
    if (version !== modelLoadVersion.value) return
    models.value = (available as Array<ClaudeModel & { id?: string; display_name?: string }>).reduce<ModelOption[]>((items, model) => {
      const id = String(model.id ?? '').trim()
      if (!id || items.some(item => item.id === id)) return items
      items.push({ id, label: String(model.display_name || id) })
      return items
    }, [])
    // Composite groups cannot resolve an empty public model. Start them on
    // the first candidate while keeping the explicit “Automatic” option for
    // operators who want to clear the selection manually.
    if (testMode.value === 'group' && selectedGroup.value?.platform === 'composite' && models.value.length > 0) {
      selectedModelId.value = models.value[0].id
    }
  } catch (error) {
    if (version === modelLoadVersion.value) {
      models.value = []
      appStore.showError(errorText(error))
    }
  } finally {
    if (version === modelLoadVersion.value) loadingModels.value = false
  }
}

async function refreshAll() {
  await Promise.all([loadAccounts(), loadGroups()])
  await loadModels()
}

function readHistory() {
  try {
    const raw = localStorage.getItem(HISTORY_KEY)
    if (!raw) return
    const parsed = JSON.parse(raw)
    if (Array.isArray(parsed)) history.value = parsed.slice(0, MAX_HISTORY)
  } catch {
    history.value = []
  }
}

function writeHistory() {
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(history.value.slice(0, MAX_HISTORY)))
  } catch {
    // History is a convenience; a full or disabled localStorage must not block testing.
  }
}

function appendHistory(entry: HistoryEntry) {
  history.value = [entry, ...history.value].slice(0, MAX_HISTORY)
  writeHistory()
}

async function runTest() {
  const target = testMode.value === 'account' ? selectedAccount.value : selectedGroup.value
  if (!target || running.value) return
  const controller = new AbortController()
  testController.value = controller
  running.value = true
  errorMessage.value = ''
  result.value = null
  const testPrompt = prompt.value.trim() || 'hello'
  prompt.value = testPrompt
  try {
    const payload = { model_id: selectedModelId.value || undefined, prompt: testPrompt }
    const options = { signal: controller.signal }
    const debugResult = testMode.value === 'account'
      ? await adminAPI.accounts.debugTestAccount(target.id, payload, options)
      : await adminAPI.accounts.debugTestGroup(target.id, payload, options)
    result.value = debugResult
    // History is kept in browser storage for convenience; do not persist the
    // raw event stream or a potentially large upstream payload.
    const historyResult: AccountDebugTestResult = { ...debugResult, raw_response: undefined }
    appendHistory({
      id: `${Date.now()}-${testMode.value}-${target.id}`,
      createdAt: new Date().toISOString(),
      accountId: debugResult.account_id || (testMode.value === 'account' ? target.id : 0),
      accountName: debugResult.account_name || (testMode.value === 'account' ? target.name : ''),
      targetName: testMode.value === 'group' && debugResult.account_name ? `${target.name} → ${debugResult.account_name}` : target.name,
      targetType: testMode.value,
      model: debugResult.model,
      result: historyResult
    })
    appStore.showSuccess(t('admin.channelTest.testSucceeded'))
  } catch (error) {
    if (controller.signal.aborted) {
      errorMessage.value = t('admin.channelTest.stopped')
    } else {
      errorMessage.value = errorText(error)
      appendHistory({
        id: `${Date.now()}-${testMode.value}-${target.id}`,
        createdAt: new Date().toISOString(),
        accountId: testMode.value === 'account' ? target.id : 0,
        accountName: testMode.value === 'account' ? target.name : '',
        targetName: target.name,
        targetType: testMode.value,
        model: selectedModelId.value,
        result: null,
        error: errorMessage.value
      })
    }
  } finally {
    if (testController.value === controller) testController.value = null
    running.value = false
  }
}

function stopTest() {
  testController.value?.abort()
}

function clearHistory() {
  history.value = []
  writeHistory()
}

function openHistory(entry: HistoryEntry) {
  if (entry.result) {
    result.value = entry.result
    errorMessage.value = ''
  } else {
    result.value = null
    errorMessage.value = entry.error || t('admin.channelTest.requestFailed')
  }
}

function formatMs(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return value < 1000 ? `${Math.max(0, Math.round(value))} ms` : `${(value / 1000).toFixed(2)} s`
}

function formatTokens(value: number | undefined): string {
  return Number.isFinite(value) ? new Intl.NumberFormat().format(value || 0) : '—'
}

function formatCost(value: number | undefined, source: string | undefined): string {
  if (source === 'unavailable' || !Number.isFinite(value)) return t('admin.channelTest.costUnavailable')
  const amount = `$${(value || 0).toFixed(6)}`
  return source && source !== 'catalog' ? `${amount} (${t('admin.channelTest.costEstimated')})` : amount
}

function formatDate(value: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value))
  } catch {
    return value
  }
}

function usageSourceLabel(source: string | undefined): string {
  if (source === 'upstream') return t('admin.channelTest.usageUpstream')
  if (source === 'mixed') return t('admin.channelTest.usageMixed')
  return t('admin.channelTest.usageEstimated')
}

watch([testMode, selectedAccountId, selectedGroupId], () => {
  void loadModels()
})

onMounted(async () => {
  readHistory()
  await Promise.all([loadAccounts(), loadGroups()])
  await loadModels()
})

onUnmounted(() => {
  testController.value?.abort()
  modelLoadVersion.value += 1
})
</script>
