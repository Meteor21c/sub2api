<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('modelTests.title') }}</h1>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('modelTests.description') }}</p>
        </div>
        <div class="flex items-center gap-2">
          <span class="rounded-full bg-primary-50 px-3 py-1.5 text-xs font-medium text-primary-700 dark:bg-primary-500/10 dark:text-primary-300">{{ t('modelTests.schedule') }}</span>
          <button class="btn btn-secondary" :disabled="loading" :aria-label="t('modelTests.refresh')" @click="load(true)"><Icon name="refresh" size="sm" /></button>
        </div>
      </div>

      <div class="flex flex-wrap gap-1 rounded-xl bg-gray-100 p-1 dark:bg-dark-900" role="tablist" :aria-label="t('modelTests.title')">
        <button v-for="item in tabs" :id="`model-tests-tab-${item.id}`" :key="item.id" role="tab" :aria-selected="tab === item.id" aria-controls="model-tests-panel" class="rounded-lg px-5 py-2.5 text-sm font-medium transition" :class="tab === item.id ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'" @click="setTab(item.id)">{{ item.label }}</button>
      </div>

      <div class="flex items-center gap-2 text-xs font-medium text-amber-700 dark:text-amber-300">
        <Icon name="clock" size="sm" />{{ t('modelTests.retention') }}
      </div>

      <div id="model-tests-panel" role="tabpanel" :aria-labelledby="`model-tests-tab-${tab}`" class="space-y-6">
        <template v-if="tab === 'automatic'">
          <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <div v-for="stat in stats" :key="stat.label" class="card p-4 sm:p-5">
              <p class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ stat.label }}</p>
              <p class="mt-2 text-2xl font-semibold tracking-tight">{{ stat.value }}</p>
              <p class="mt-1 min-h-4 text-xs text-gray-400">{{ stat.hint }}</p>
            </div>
          </div>
          <div v-if="!overview.plans.length" class="card p-12 text-center">
            <Icon name="chart" size="lg" class="mx-auto mb-4 text-primary-500" />
            <h2 class="font-semibold">{{ t('modelTests.noPlans') }}</h2>
            <p class="mt-2 text-sm text-gray-500">{{ t('modelTests.noPlansHint') }}</p>
            <button v-if="auth.isAdmin" class="btn btn-primary mt-5" @click="setTab('manage')">{{ t('modelTests.add') }}</button>
          </div>
          <section v-for="plan in overview.plans" :key="plan.id" class="card overflow-hidden">
            <div class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 p-5 dark:border-dark-700">
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h2 class="text-base font-semibold">{{ plan.label }}</h2>
                  <span class="rounded-md bg-primary-50 px-2 py-1 text-xs text-primary-700 dark:bg-primary-500/10 dark:text-primary-300">{{ kindLabel(plan.test_kind) }}</span>
                  <span v-if="!plan.enabled" class="text-xs text-gray-400">{{ t('modelTests.paused') }}</span>
                </div>
                <p class="mt-2 break-all font-mono text-xs text-gray-500">{{ plan.model }}</p>
              </div>
              <div class="text-right text-xs text-gray-500">
                <p>{{ t('modelTests.next') }} · {{ plan.enabled ? date(plan.next_run_at) : t('modelTests.paused') }}</p>
                <p class="mt-2">{{ t('modelTests.latest') }} · {{ date(planJobs(plan.id)[0]?.created_at) }}</p>
              </div>
            </div>
            <div class="space-y-4 p-5">
              <div class="flex flex-wrap items-center justify-between gap-3">
                <div v-if="planJobs(plan.id)[0]" class="flex flex-wrap items-center gap-3">
                  <span class="rounded-full px-2.5 py-1 text-xs font-medium" :class="statusClass(planJobs(plan.id)[0]!)">{{ statusLabel(planJobs(plan.id)[0]!) }}</span>
                  <span class="text-sm text-gray-500">{{ duration(planJobs(plan.id)[0]!.total_ms) }}</span>
                </div>
                <span v-else class="text-sm text-gray-400">{{ t('modelTests.waiting') }}</span>
                <button v-if="planJobs(plan.id)[0]" class="text-sm font-medium text-primary-600 hover:underline dark:text-primary-300" @click="showResult(planJobs(plan.id)[0]!)">{{ t('modelTests.view') }} <span aria-hidden="true">↗</span></button>
              </div>
              <div>
                <div class="mb-2 flex justify-between text-[11px] text-gray-400"><span>{{ t('modelTests.history') }}</span><span>{{ date(now) }}</span></div>
                <div class="flex min-h-8 gap-1.5" :aria-label="t('modelTests.history')">
                  <button v-for="job in [...planJobs(plan.id)].reverse()" :key="job.id" class="h-8 min-w-3 max-w-14 flex-1 rounded-md transition hover:-translate-y-0.5 hover:ring-2 hover:ring-primary-400" :class="barClass(job)" :title="`${date(job.created_at)} · ${statusLabel(job)} · ${duration(job.total_ms)}`" :aria-label="`${date(job.created_at)} ${statusLabel(job)}`" @click="showResult(job)" />
                  <span v-if="!planJobs(plan.id).length" class="h-8 w-full rounded-md bg-gray-100 dark:bg-dark-800" />
                </div>
              </div>
            </div>
          </section>
          <details class="card p-5 text-sm text-gray-500">
            <summary class="cursor-pointer font-medium text-gray-700 dark:text-gray-200">{{ t('modelTests.candyPrompt') }} / {{ t('modelTests.pelicanPrompt') }}</summary>
            <p class="mt-4 font-semibold">{{ t('modelTests.candy') }}</p><p class="mt-2 leading-7">{{ QUICK_TEST_PROMPTS.candy }}</p>
            <p class="mt-3 text-xs">{{ t('modelTests.benchmarkHint') }}</p>
            <p class="mt-5 font-semibold">{{ t('modelTests.pelican') }}</p><p class="mt-2 leading-7">{{ QUICK_TEST_PROMPTS.pelican }}</p>
          </details>
        </template>

        <template v-else-if="tab === 'mine'">
          <form class="card space-y-5 p-5 sm:p-6" @submit.prevent="submit">
            <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-[1fr_1fr_1fr_auto] xl:items-end">
              <label class="space-y-2 text-sm font-medium"><span>{{ t('modelTests.key') }}</span>
                <select v-model="keyId" class="input" required :disabled="submitting"><option :value="0" disabled>{{ t('modelTests.selectKey') }}</option><option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name }}{{ key.group_name ? ` · ${key.group_name}` : '' }}</option></select>
              </label>
              <label class="space-y-2 text-sm font-medium"><span>{{ t('modelTests.model') }}</span>
                <select v-model="model" class="input" required :disabled="!keyId || modelsLoading || submitting"><option value="" disabled>{{ modelsLoading ? t('modelTests.loadingModels') : t('modelTests.selectModel') }}</option><option v-for="name in models" :key="name" :value="name">{{ name }}</option></select>
              </label>
              <label class="space-y-2 text-sm font-medium"><span>{{ t('modelTests.kind') }}</span>
                <select v-model="kind" class="input" :disabled="submitting"><option value="pelican">{{ t('modelTests.pelican') }}</option><option value="candy">{{ t('modelTests.candy') }}</option></select>
              </label>
              <button class="btn btn-primary justify-center" type="submit" :disabled="!keyId || !model || submitting || pendingCount >= 2"><Icon name="play" size="sm" />{{ submitting ? t('modelTests.submitting') : t('modelTests.start') }}</button>
            </div>
            <p v-if="!keys.length" class="text-sm text-amber-700 dark:text-amber-300">{{ t('modelTests.noKeys') }} <RouterLink to="/keys" class="underline">{{ t('modelTests.createKey') }}</RouterLink></p>
            <p v-else-if="keyId && !modelsLoading && !models.length" class="text-xs text-amber-700">{{ t('modelTests.noModels') }}</p>
            <div class="rounded-xl bg-primary-50/60 p-4 text-sm dark:bg-primary-500/5">
              <p class="font-medium text-primary-800 dark:text-primary-200">{{ t('modelTests.billed') }}</p>
              <p class="mt-1 text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t('modelTests.background') }}</p>
            </div>
          </form>
          <p v-if="pendingCount" class="flex items-center gap-2 text-sm text-primary-600 dark:text-primary-300" role="status"><span class="h-2 w-2 animate-pulse rounded-full bg-primary-500" />{{ t('modelTests.pending', { count: pendingCount }) }}</p>
          <div v-if="!mine.length" class="card p-12 text-center"><h2 class="font-semibold">{{ t('modelTests.noMine') }}</h2><p class="mt-2 text-sm text-gray-500">{{ t('modelTests.noMineHint') }}</p></div>
          <div v-else class="card divide-y divide-gray-100 overflow-hidden dark:divide-dark-700">
            <button v-for="job in mine" :key="job.id" class="flex w-full flex-wrap items-center justify-between gap-4 px-5 py-4 text-left transition hover:bg-gray-50 dark:hover:bg-dark-800/60" @click="showResult(job)">
              <div class="min-w-0 flex-1"><p class="font-medium">{{ kindLabel(job.test_kind) }} <span class="ml-2 text-xs font-normal text-gray-400">{{ job.label }}</span></p><p class="mt-1 break-all font-mono text-xs text-gray-500">{{ job.model }}</p><p class="mt-2 text-xs text-gray-400">{{ date(job.created_at) }} · {{ duration(job.total_ms) }}</p></div>
              <span class="rounded-full px-3 py-1.5 text-xs font-medium" :class="statusClass(job)">{{ statusLabel(job) }}</span>
              <Icon name="chevronRight" size="sm" class="text-gray-400" />
            </button>
          </div>
        </template>

        <template v-else-if="auth.isAdmin">
          <div class="flex flex-wrap items-start justify-between gap-4"><p class="max-w-2xl text-sm leading-7 text-gray-500">{{ t('modelTests.configHint') }}</p><button class="btn btn-primary" @click="editPlan(null)"><Icon name="plus" size="sm" />{{ t('modelTests.add') }}</button></div>
          <div v-if="!overview.plans.length" class="card p-10 text-center text-sm text-gray-500">{{ t('modelTests.noPlans') }}</div>
          <div v-for="plan in overview.plans" :key="plan.id" class="card flex flex-wrap items-center justify-between gap-4 p-5">
            <div><h2 class="font-medium">{{ plan.label }} <span class="ml-2 text-xs text-gray-400">{{ kindLabel(plan.test_kind) }}</span></h2><p class="mt-2 break-all text-xs text-gray-500">{{ plan.model }}</p><p class="mt-2 text-xs text-gray-400">{{ t('modelTests.next') }} · {{ plan.enabled ? date(plan.next_run_at) : t('modelTests.paused') }}</p></div>
            <div class="flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" :disabled="planBusy" @click="togglePlan(plan)">{{ plan.enabled ? t('modelTests.disable') : t('modelTests.enable') }}</button><button class="btn btn-secondary btn-sm" :disabled="planBusy" @click="editPlan(plan)">{{ t('modelTests.edit') }}</button><button class="btn btn-secondary btn-sm text-red-600" :disabled="planBusy" @click="deleteTarget = plan">{{ t('modelTests.remove') }}</button></div>
          </div>
        </template>
      </div>
    </div>

    <BaseDialog :show="resultOpen" :title="t('modelTests.result')" width="extra-wide" @close="closeResult">
      <p v-if="detailLoading" class="py-12 text-center text-gray-500">{{ t('common.loading') }}</p>
      <div v-else-if="selected" class="space-y-5">
        <div class="flex flex-wrap items-center justify-between gap-3"><div><p class="font-semibold">{{ selected.label }} · {{ kindLabel(selected.test_kind) }}</p><p class="mt-1 break-all font-mono text-xs text-gray-500">{{ selected.model }}</p></div><span class="rounded-full px-3 py-1.5 text-xs" :class="statusClass(selected)">{{ statusLabel(selected) }}</span></div>
        <dl class="grid grid-cols-2 gap-4 rounded-xl bg-gray-50 p-4 text-sm dark:bg-dark-800"><div><dt class="text-xs text-gray-400">{{ t('modelTests.created') }}</dt><dd class="mt-1">{{ date(selected.created_at) }}</dd></div><div><dt class="text-xs text-gray-400">{{ t('modelTests.expires') }}</dt><dd class="mt-1">{{ date(selected.expires_at) }}</dd></div><div><dt class="text-xs text-gray-400">{{ t('modelTests.time') }}</dt><dd class="mt-1">{{ duration(selected.total_ms) }}</dd></div><div v-if="selected.input_tokens || selected.output_tokens"><dt class="text-xs text-gray-400">{{ t('modelTests.tokens') }}</dt><dd class="mt-1">{{ selected.input_tokens.toLocaleString() }} → {{ selected.output_tokens.toLocaleString() }}</dd></div></dl>
        <p v-if="selected.status === 'queued' || selected.status === 'running'" class="rounded-xl bg-primary-50 p-4 text-sm text-primary-700 dark:bg-primary-500/10 dark:text-primary-200" role="status">{{ t('modelTests.background') }}</p>
        <p v-if="selected.error_code" class="rounded-xl bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-500/10 dark:text-amber-200">{{ errorLabel(selected.error_code) }}</p>
        <template v-if="selected.test_kind === 'pelican' && selected.content">
          <div v-if="svgPreview?.src && !previewFailed" class="space-y-3"><img :src="svgPreview.src" :alt="t('modelTests.pelican')" class="max-h-[65vh] min-h-60 w-full rounded-xl border border-gray-200 bg-white object-contain dark:border-dark-600" data-testid="model-test-svg" @error="previewFailed = true" /><div class="flex flex-wrap items-center justify-between gap-2"><p class="text-xs text-gray-500">{{ t('modelTests.svgHint') }}</p><a :href="svgPreview.src" download="meteor-pelican.svg" class="text-sm text-primary-600 hover:underline dark:text-primary-300">{{ t('modelTests.download') }}</a></div></div>
          <p v-else class="text-sm text-amber-700">{{ t('modelTests.svgInvalid') }}</p>
        </template>
        <div v-if="selected.test_kind === 'candy' && selected.status === 'succeeded'" class="space-y-3 rounded-xl border border-gray-200 p-5 dark:border-dark-700"><p class="text-lg font-semibold">{{ statusLabel(selected) }}</p><div class="flex flex-wrap gap-5 text-sm"><span>{{ t('modelTests.answer') }}: <strong>{{ selected.answer ?? '—' }}</strong></span><span>{{ t('modelTests.expected') }}</span></div><p class="text-xs leading-6 text-gray-500">{{ t('modelTests.explanation') }}</p><p class="text-xs text-gray-400">{{ t('modelTests.benchmarkHint') }}</p></div>
        <details v-if="selected.content" :open="selected.test_kind === 'candy'" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer text-sm font-medium">{{ t('modelTests.raw') }}</summary><pre class="mt-4 max-h-96 overflow-auto whitespace-pre-wrap break-words font-sans text-sm leading-7">{{ selected.content }}</pre></details>
        <div v-if="selected.source === 'user'" class="flex flex-wrap items-center justify-between gap-3 text-xs text-gray-500"><span>{{ t('modelTests.tokensHint') }}</span><RouterLink to="/usage" class="text-primary-600 underline dark:text-primary-300">{{ t('modelTests.seeUsage') }}</RouterLink></div>
      </div>
      <p v-else class="py-10 text-center text-gray-500">{{ t('modelTests.resultExpired') }}</p>
    </BaseDialog>

    <BaseDialog :show="planOpen && auth.isAdmin" :title="t(editingId ? 'modelTests.edit' : 'modelTests.add')" width="wide" @close="planOpen = false">
      <form class="space-y-5" @submit.prevent="savePlan">
        <label class="block space-y-2 text-sm font-medium"><span>{{ t('modelTests.label') }}</span><input v-model="planForm.label" class="input" maxlength="80" required :disabled="planBusy" /></label>
        <div class="grid gap-4 sm:grid-cols-2"><label class="space-y-2 text-sm font-medium"><span>{{ t('modelTests.group') }}</span><select v-model="planForm.group_id" class="input" required :disabled="planBusy"><option :value="0" disabled>{{ t('modelTests.group') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label><label class="space-y-2 text-sm font-medium"><span>{{ t('modelTests.kind') }}</span><select v-model="planForm.test_kind" class="input" :disabled="planBusy"><option value="pelican">{{ t('modelTests.pelican') }}</option><option value="candy">{{ t('modelTests.candy') }}</option></select></label></div>
        <label class="block space-y-2 text-sm font-medium"><span>{{ t('modelTests.model') }}</span><input v-model="planForm.model" list="model-test-plan-models" class="input" maxlength="200" required :disabled="planBusy" /><datalist id="model-test-plan-models"><option v-for="name in planModels" :key="name" :value="name" /></datalist></label>
        <label class="flex items-center gap-3 rounded-xl bg-gray-50 p-4 text-sm dark:bg-dark-800"><input v-model="planForm.enabled" type="checkbox" class="h-4 w-4 rounded" :disabled="planBusy" /><span>{{ t('modelTests.enabled') }}</span></label>
        <p class="text-xs leading-6 text-gray-500">{{ t('modelTests.enableHint') }}</p>
        <div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary" @click="planOpen = false">{{ t('modelTests.cancel') }}</button><button type="submit" class="btn btn-primary" :disabled="planBusy || !planForm.group_id || !planForm.model.trim() || !planForm.label.trim()">{{ t('modelTests.save') }}</button></div>
      </form>
    </BaseDialog>
    <BaseDialog :show="Boolean(deleteTarget)" :title="t('modelTests.remove')" width="narrow" @close="deleteTarget = null"><p class="text-sm">{{ t('modelTests.removeConfirm') }}</p><template #footer><button class="btn btn-secondary" @click="deleteTarget = null">{{ t('modelTests.cancel') }}</button><button class="btn btn-danger" :disabled="planBusy" @click="removePlan">{{ t('modelTests.remove') }}</button></template></BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { prepareSvgPreview, QUICK_TEST_PROMPTS } from '@/utils/availabilityQuickTests'
import { getCatalog } from '@/api/admin/availability'
import { modelTestsAPI, type ModelTestJob, type ModelTestKind, type ModelTestPlan, type ModelTestPlanInput, type ModelTestOverview, type ModelTestKey, type ModelTestGroup } from '@/api/modelTests'

const { t } = useI18n()
const auth = useAuthStore()
const app = useAppStore()
const route = useRoute()
const router = useRouter()
type Tab = 'automatic' | 'mine' | 'manage'
const tab = ref<Tab>(route.path.startsWith('/admin/') ? 'manage' : route.query.tab === 'mine' ? 'mine' : 'automatic')
const tabs = computed(() => [{ id: 'automatic' as Tab, label: t('modelTests.automatic') }, { id: 'mine' as Tab, label: t('modelTests.mine') }, ...(auth.isAdmin ? [{ id: 'manage' as Tab, label: t('modelTests.manage') }] : [])])
const overview = ref<ModelTestOverview>({ plans: [], jobs: [], interval_hours: 2, retention_hours: 24 })
const mine = ref<ModelTestJob[]>([])
const keys = ref<ModelTestKey[]>([])
const groups = ref<ModelTestGroup[]>([])
const loading = ref(false)
const keyId = ref(0)
const model = ref('')
const models = ref<string[]>([])
const modelsLoading = ref(false)
const kind = ref<ModelTestKind>('pelican')
const submitting = ref(false)
const resultOpen = ref(false)
const detailLoading = ref(false)
const selected = ref<ModelTestJob | null>(null)
const previewFailed = ref(false)
const now = ref(new Date().toISOString())
const planOpen = ref(false)
const editingId = ref<number | null>(null)
const planForm = reactive<ModelTestPlanInput>({ label: '', group_id: 0, model: '', test_kind: 'pelican', enabled: false })
const planModels = ref<string[]>([])
const planBusy = ref(false)
const deleteTarget = ref<ModelTestPlan | null>(null)
let poll: ReturnType<typeof setTimeout> | undefined
let disposed = false
let modelVersion = 0
let detailVersion = 0
let planModelVersion = 0
let submission: { signature: string; id: string } | null = null

const pendingCount = computed(() => mine.value.filter(j => j.status === 'queued' || j.status === 'running').length)
const svgPreview = computed(() => selected.value?.test_kind === 'pelican' && selected.value.content ? prepareSvgPreview(selected.value.content) : null)
const planJobs = (id: number) => overview.value.jobs.filter(job => job.plan_id === id)
const stats = computed(() => {
  const jobs = overview.value.jobs
  const completed = jobs.filter(j => j.status === 'succeeded')
  const graded = completed.filter(j => j.test_kind === 'candy' && j.verdict !== 'ungraded')
  return [
    { label: t('modelTests.groups'), value: String(new Set(overview.value.plans.filter(p => p.enabled).map(p => p.group_id)).size), hint: t('modelTests.history') },
    { label: t('modelTests.candyRate'), value: graded.length ? `${Math.round(graded.filter(j => j.verdict === 'correct').length / graded.length * 100)}%` : '—', hint: graded.length ? t('modelTests.samples', { count: graded.length }) : t('modelTests.noSamples') },
    { label: t('modelTests.averageTime'), value: completed.length ? duration(completed.reduce((sum, j) => sum + j.total_ms, 0) / completed.length) : '—', hint: t('modelTests.history') },
    { label: t('modelTests.failures'), value: String(jobs.filter(j => j.status === 'failed' || j.status === 'interrupted').length), hint: t('modelTests.history') }
  ]
})
function kindLabel(value: ModelTestKind) { return t(value === 'candy' ? 'modelTests.candy' : 'modelTests.pelican') }
function statusLabel(job: ModelTestJob) { return t(`modelTests.${job.status === 'succeeded' && job.test_kind === 'candy' ? job.verdict : job.status}`) }
function statusClass(job: ModelTestJob) {
  if (job.status === 'queued' || job.status === 'running') return 'bg-blue-50 text-blue-700 dark:bg-blue-500/10 dark:text-blue-300'
  if (job.status !== 'succeeded' || job.verdict === 'incorrect') return 'bg-rose-50 text-rose-700 dark:bg-rose-500/10 dark:text-rose-300'
  if (job.test_kind === 'candy' && job.verdict === 'ungraded') return 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300'
  return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300'
}
function barClass(job: ModelTestJob) {
  if (job.status === 'queued' || job.status === 'running') return 'animate-pulse bg-blue-400'
  if (job.status !== 'succeeded' || job.verdict === 'incorrect') return 'bg-rose-400 dark:bg-rose-500'
  if (job.test_kind === 'candy' && job.verdict === 'ungraded') return 'bg-amber-400'
  return 'bg-emerald-400 dark:bg-emerald-500'
}
function duration(ms: number) { return ms > 0 ? `${(ms / 1000).toFixed(1)}s` : '—' }
function date(value?: string | null) { return value ? new Date(value).toLocaleString(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—' }
const errorCodes = new Set(['key_unavailable', 'key_changed', 'key_rejected', 'balance_or_quota', 'rate_limited', 'model_unavailable', 'group_unavailable', 'timeout', 'interrupted', 'plan_changed', 'output_too_large', 'output_incomplete', 'empty_output', 'service_unavailable', 'request_failed'])
function errorLabel(code: string) { return t(`modelTests.errors.${errorCodes.has(code) ? code : 'request_failed'}`) }
function notifyError(error: unknown) { app.showError(extractApiErrorMessage(error)) }
function setTab(value: Tab) {
  tab.value = value
  void router.replace(value === 'manage' ? '/admin/model-tests' : { path: '/model-tests', query: value === 'mine' ? { tab: 'mine' } : {} })
}
watch(() => route.fullPath, () => { tab.value = route.path.startsWith('/admin/') ? 'manage' : route.query.tab === 'mine' ? 'mine' : 'automatic' })

async function load(showError = false) {
  if (loading.value || disposed) return
  loading.value = true
  try {
    const results = await Promise.allSettled([modelTestsAPI.overview(), modelTestsAPI.mine()])
    if (disposed) return
    if (results[0].status === 'fulfilled') overview.value = results[0].value
    else if (showError) notifyError(results[0].reason)
    if (results[1].status === 'fulfilled') mine.value = results[1].value
    else if (showError) notifyError(results[1].reason)
    now.value = new Date().toISOString()
    // Enforce expiry in the open page too; an expired preview must not linger.
    if (selected.value && Date.parse(selected.value.expires_at) <= Date.now()) closeResult()
    else if (selected.value && resultOpen.value && ['queued', 'running'].includes(selected.value.status)) {
      const current = [...mine.value, ...overview.value.jobs].find(job => job.id === selected.value?.id)
      if (current && current.status !== selected.value.status) await showResult(current, true)
    }
  } finally { loading.value = false }
}
async function loadKeys() {
  try { keys.value = await modelTestsAPI.keys() } catch (error) { notifyError(error) }
}
watch(keyId, async id => {
  const version = ++modelVersion
  model.value = ''; models.value = []
  if (!id) return
  modelsLoading.value = true
  try {
    const names = await modelTestsAPI.models(id)
    if (version === modelVersion && !disposed) { models.value = names; model.value = names[0] ?? '' }
  } catch (error) { if (version === modelVersion && !disposed) notifyError(error) }
  finally { if (version === modelVersion) modelsLoading.value = false }
})
async function submit() {
  if (submitting.value || !keyId.value || !model.value) return
  submitting.value = true
  const signature = `${keyId.value}:${model.value}:${kind.value}`
  if (!submission || submission.signature !== signature) submission = { signature, id: crypto.randomUUID() }
  try {
    await modelTestsAPI.submit({ api_key_id: keyId.value, model: model.value, test_kind: kind.value, client_request_id: submission.id })
    submission = null
    app.showSuccess(t('modelTests.submitted'))
    await load(true)
  } catch (error) { notifyError(error) }
  finally { submitting.value = false }
}
async function showResult(job: ModelTestJob, silent = false) {
  const version = ++detailVersion
  if (!silent) { selected.value = null; previewFailed.value = false; detailLoading.value = true; resultOpen.value = true }
  try {
    const detail = await modelTestsAPI.detail(job.id, job.source)
    if (version === detailVersion && !disposed) { selected.value = detail; previewFailed.value = false }
  } catch (error) { if (version === detailVersion && !disposed) { selected.value = null; if (!silent) notifyError(error) } }
  finally { if (version === detailVersion) detailLoading.value = false }
}
function closeResult() { detailVersion++; resultOpen.value = false; selected.value = null }
async function editPlan(plan: ModelTestPlan | null) {
  editingId.value = plan?.id ?? null
  Object.assign(planForm, plan ? { label: plan.label, group_id: plan.group_id, model: plan.model, test_kind: plan.test_kind, enabled: plan.enabled } : { label: '', group_id: 0, model: '', test_kind: 'pelican', enabled: false })
  planOpen.value = true
  try { groups.value = await modelTestsAPI.groups() } catch (error) { notifyError(error) }
}
watch(() => planForm.group_id, async id => {
  const version = ++planModelVersion
  planModels.value = []
  if (!id || !auth.isAdmin) return
  try {
    const catalog = await getCatalog(id)
    if (version === planModelVersion && !disposed) planModels.value = catalog.models.filter(m => m.downstream_allowed !== false).map(m => m.id)
  } catch (error) { if (version === planModelVersion && !disposed) notifyError(error) }
})
async function savePlan() {
  if (planBusy.value) return
  planBusy.value = true
  try { await modelTestsAPI.savePlan(editingId.value, { ...planForm }); planOpen.value = false; app.showSuccess(t('modelTests.saved')); await load(true) }
  catch (error) { notifyError(error) }
  finally { planBusy.value = false }
}
async function togglePlan(plan: ModelTestPlan) {
  planBusy.value = true
  try { await modelTestsAPI.savePlan(plan.id, { label: plan.label, group_id: plan.group_id, model: plan.model, test_kind: plan.test_kind, enabled: !plan.enabled }); await load(true) }
  catch (error) { notifyError(error) }
  finally { planBusy.value = false }
}
async function removePlan() {
  if (!deleteTarget.value) return
  planBusy.value = true
  try { await modelTestsAPI.deletePlan(deleteTarget.value.id); deleteTarget.value = null; app.showSuccess(t('modelTests.removed')); await load(true) }
  catch (error) { notifyError(error) }
  finally { planBusy.value = false }
}
function visibilityChanged() { if (!document.hidden) void load() }
function schedulePoll() {
  poll = setTimeout(async () => { if (!document.hidden) await load(); if (!disposed) schedulePoll() }, pendingCount.value ? 4000 : 20000)
}
onMounted(() => { void load(true); void loadKeys(); schedulePoll(); document.addEventListener('visibilitychange', visibilityChanged) })
onUnmounted(() => { disposed = true; detailVersion++; modelVersion++; planModelVersion++; clearTimeout(poll); document.removeEventListener('visibilitychange', visibilityChanged) })
</script>
