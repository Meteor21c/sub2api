<template>
  <AppLayout>
    <div class="business-monitor mx-auto max-w-[1600px] space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900 dark:text-white">经营监测</h1>
          <p class="mt-2 text-base text-gray-600 dark:text-gray-300">用本站扣费、渠道倍率和上游余额变化，查看经营情况与消耗偏差。</p>
        </div>
        <button class="btn btn-primary min-h-[44px]" :disabled="busy" @click="refreshToday">
          {{ refreshing ? '正在采集并计算…' : '更新余额并计算今日' }}
        </button>
      </header>

      <section class="card p-5" aria-labelledby="balance-panel-title">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div><h2 id="balance-panel-title" class="text-lg font-semibold">上游余额</h2><p class="mt-1 text-sm text-gray-500">仅站点所有者可见 · 原币种展示 · 最多复用15秒 · {{ balances ? businessTime(balances.generatedAt) : '尚未读取' }}</p></div>
          <button class="btn btn-secondary min-h-[44px]" :disabled="balanceLoading" @click="loadBalances">{{ balanceLoading ? '正在读取…' : '刷新余额' }}</button>
        </div>
        <p v-if="balanceError" role="alert" class="mt-3 text-red-600">{{ balanceError }}</p>
        <div v-if="balances" class="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          <article v-for="account in balances.accounts" :key="account.id" class="rounded-lg border border-gray-100 p-4 dark:border-dark-600">
            <div class="flex items-center justify-between gap-2"><h3 class="font-medium">{{ account.name }}</h3><span class="text-sm" :class="account.status === 'ready' ? 'text-emerald-600' : 'text-amber-700 dark:text-amber-400'">{{ balanceStatus(account.status) }}</span></div>
            <p class="mt-2 text-xl font-semibold tabular-nums">{{ balanceMoney(account.currency, account.balance) }}</p>
            <p class="mt-1 text-sm text-gray-500">{{ account.planName || '账户余额' }} · 预警线 {{ balanceMoney(account.currency, account.threshold) }}</p>
            <p class="mt-1 break-all text-sm text-gray-500">{{ account.baseUrl }}</p>
          </article>
        </div>
      </section>

      <section class="card flex flex-wrap items-end justify-between gap-4 p-5">
        <div class="flex flex-wrap items-end gap-4">
          <div>
            <span class="mb-2 block font-medium">统计周期</span>
            <div class="flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700" aria-label="统计周期">
              <button v-for="item in periods" :key="item.value" class="rounded-md px-5 py-2" :class="period === item.value ? 'bg-white font-semibold text-primary-600 shadow-sm dark:bg-dark-600' : 'text-gray-600 dark:text-gray-300'" :aria-pressed="period === item.value" :disabled="busy" @click="selectPeriod(item.value)">{{ item.label }}</button>
            </div>
          </div>
          <label class="block">
            <span class="mb-2 block font-medium">日期／周期内任一天</span>
            <input v-model="date" type="date" class="input min-h-[44px]" :max="today" :disabled="busy" @change="loadReport" />
          </label>
          <button class="btn btn-secondary min-h-[44px]" :disabled="busy" @click="loadReport">查看报表</button>
        </div>
        <div class="text-sm leading-6 text-gray-500 dark:text-gray-400">
          <p>后台每5分钟采样 · 北京时间日终结算</p>
          <p>查看历史不查询上游 · 人民币／美元按1:1记账</p>
        </div>
      </section>

      <div v-if="error" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-4 text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-300">{{ error }}</div>
      <div v-if="loading && !report" class="card p-12 text-center text-gray-500">正在读取经营报表…</div>

      <template v-if="report">
        <div class="flex flex-wrap items-center justify-between gap-3 text-sm text-gray-500 dark:text-gray-400">
          <p>{{ rangeLabel }} · {{ report.finalized ? '已保存日终结果' : '截至当前的估算' }} · 计算于 {{ businessTime(report.as_of) }}</p>
          <p>用量历史始于 {{ report.coverage_start ? businessTime(report.coverage_start) : '尚无记录' }}</p>
        </div>
        <div v-if="report.summary.partial" class="rounded-xl border border-amber-200 bg-amber-50 px-5 py-4 leading-7 text-amber-900 dark:border-amber-800 dark:bg-amber-950/25 dark:text-amber-200">
          当前区间含余额缺失、暂不计入或待确认的用量。待确认绘图/视频的收入与成本均不计入预计毛利，其余待确认成本仍保留未知；余额推算消耗为钱包整体数据，不与排除后的成本直接对比。
        </div>

        <section class="grid grid-cols-2 gap-4 xl:grid-cols-4">
          <article v-for="metric in metrics" :key="metric.label" class="card p-5">
            <p class="text-base text-gray-600 dark:text-gray-300">{{ metric.label }}</p>
            <p class="my-3 break-all text-2xl font-bold tabular-nums" :class="metric.negative ? 'text-red-600 dark:text-red-400' : 'text-gray-900 dark:text-white'">{{ businessMoney(metric.value) }}</p>
            <p class="text-sm leading-6 text-gray-500 dark:text-gray-400">{{ metric.help }}</p>
          </article>
        </section>
        <div class="card flex flex-wrap items-center gap-x-8 gap-y-3 px-5 py-4">
          <p>异常线索 <strong :class="report.summary.anomaly_count ? 'text-red-600' : 'text-emerald-600'">{{ report.summary.anomaly_count }}</strong></p>
          <p>已覆盖余额毛利 <strong>{{ businessMoney(report.summary.observed_profit) }}</strong></p>
          <p>已覆盖消耗偏差 <strong>{{ businessMoney(report.summary.deviation) }}</strong></p>
          <p class="text-sm text-gray-500">套餐计量单列：{{ businessMoney(report.summary.subscription_usage) }}（不计当次现金收入）</p>
          <p v-if="report.summary.excluded_media_requests" class="text-sm text-amber-700 dark:text-amber-400">暂不计入绘图/视频：{{ report.summary.excluded_media_requests }} 次 · {{ businessMoney(report.summary.excluded_media_revenue) }} 消费；原始总消费 {{ businessMoney(report.summary.revenue) }}</p>
        </div>

        <section class="card overflow-hidden">
          <div class="border-b border-gray-100 px-5 py-5 dark:border-dark-600">
            <h2 class="text-lg font-semibold">上游消耗对比</h2>
            <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">相同 base_url 的渠道合并统计；差额超阈值提供检查线索。缺失值显示“—”，不当作零余额。</p>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full text-left text-base">
              <thead class="bg-gray-50 text-sm text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr>
                <th class="px-5 py-4">上游／状态</th><th class="px-4 py-4">计入客户消费</th><th class="px-4 py-4">计入预计成本</th><th class="px-4 py-4">钱包整体消耗</th><th class="px-4 py-4">计入预计毛利</th><th class="px-4 py-4">偏差／容差</th><th class="px-5 py-4">入账调整</th>
              </tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
                <template v-for="wallet in report.wallets" :key="wallet.key">
                  <tr class="align-top">
                    <td class="min-w-[240px] px-5 py-4"><p class="font-semibold">{{ wallet.name || wallet.base_url }}</p><p class="mt-1 break-all text-sm text-gray-500">{{ wallet.base_url }}</p><div class="mt-2 flex flex-wrap gap-2"><span class="rounded-full px-2.5 py-1 text-sm" :class="statusClass(wallet.status)">{{ statusText(wallet.status) }}</span><span class="rounded-full bg-gray-100 px-2.5 py-1 text-sm text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ wallet.account_ids?.length || 0 }} 个渠道</span></div></td>
                    <td class="whitespace-nowrap px-4 py-4 tabular-nums">{{ businessMoney(businessIncludedRevenue(wallet.revenue, wallet.excluded_media_revenue)) }}<p v-if="wallet.excluded_media_requests" class="mt-2 text-sm text-amber-700 dark:text-amber-400">暂不计入 {{ businessMoney(wallet.excluded_media_revenue) }} / {{ wallet.excluded_media_requests }} 次</p></td>
                    <td class="whitespace-nowrap px-4 py-4 tabular-nums">{{ wallet.status === 'excluded' ? '暂不计入' : wallet.expected_cost == null ? '待确认' : businessMoney(wallet.expected_cost) }}</td>
                    <td class="whitespace-nowrap px-4 py-4 tabular-nums">{{ businessMoney(wallet.balance_cost) }}<p class="mt-2 text-sm text-gray-500">{{ businessMoney(wallet.balance_start) }} → {{ businessMoney(wallet.balance_end) }}</p></td>
                    <td class="whitespace-nowrap px-4 py-4 font-semibold tabular-nums" :class="(wallet.gross_profit ?? 0) < 0 ? 'text-red-600' : ''">{{ wallet.status === 'excluded' ? '暂不计入' : wallet.gross_profit == null ? '待确认' : businessMoney(wallet.gross_profit) }}</td>
                    <td class="whitespace-nowrap px-4 py-4 tabular-nums">{{ businessMoney(wallet.deviation) }}<p class="mt-2 text-sm text-gray-500">容差 {{ businessMoney(wallet.tolerance) }}</p></td>
                    <td class="min-w-[150px] px-5 py-4"><p>{{ businessMoney(wallet.credits) }}</p><p class="mt-1 text-sm text-gray-500">{{ wallet.credit_override == null ? '自动推测净入账' : '已人工修正' }}</p><button v-if="period === 'day'" class="mt-2 text-sm font-medium text-primary-600 hover:underline" @click="openAdjustment(wallet)">修正金额</button></td>
                  </tr>
                  <tr><td colspan="7" class="px-5 pb-4 text-sm leading-6 text-gray-500 dark:text-gray-400"><div class="flex flex-wrap gap-x-5 gap-y-1"><span>最近采样 {{ businessTime(wallet.last_sample_at) }}</span><span>{{ rateText(wallet.rate_status) }}</span><span v-for="message in wallet.messages || []" :key="message" class="text-amber-700 dark:text-amber-400">{{ message }}</span></div></td></tr>
                  <tr v-if="wallet.cost_basis?.length"><td colspan="7" class="px-5 pb-4 text-sm leading-6 text-gray-500 dark:text-gray-400"><details><summary class="cursor-pointer">成本采用的上游探测倍率（不使用本地记账倍率）</summary><p v-for="rate in walletProbeRates(wallet)" :key="rate" class="mt-1">{{ rate }}</p></details></td></tr>
                </template>
                <tr v-if="!report.wallets?.length"><td colspan="7" class="p-10 text-center text-gray-500">尚无渠道用量或余额采样，首次后台采集后会自动显示。</td></tr>
              </tbody>
            </table>
          </div>
        </section>

        <div class="grid gap-6 xl:grid-cols-2">
          <section class="card p-5">
            <h2 class="text-lg font-semibold">倍率变动记录</h2>
            <p class="mt-2 text-sm leading-6 text-gray-500">记录检测到变化的时间；首次读取建立基线，不记作变更。</p>
            <ul class="mt-4 max-h-96 space-y-4 overflow-auto">
              <li v-for="change in report.rate_changes || []" :key="change.id" class="rounded-lg bg-gray-50 p-4 dark:bg-dark-800">
                <div class="flex flex-wrap justify-between gap-2"><span class="font-medium">{{ change.account_name }}</span><time class="text-sm text-gray-500">{{ businessTime(change.observed_at) }}</time></div>
                <p class="mt-2"><span class="mr-2 text-sm text-gray-500">{{ change.kind === 'site' ? '本站分组' : '上游渠道' }}</span><span class="font-mono">{{ change.old_rate }} → {{ change.new_rate }}</span></p>
              </li>
              <li v-if="!report.rate_changes?.length" class="py-8 text-center text-gray-500">此区间尚无倍率变动记录</li>
            </ul>
          </section>
          <section class="card p-5">
            <h2 class="text-lg font-semibold">充值与签到线索</h2>
            <p class="mt-2 text-sm leading-6 text-gray-500">余额正向变化只作入账推测，可能包含同时消费的影响。人工修正填写当日净入账总额。</p>
            <ul class="mt-4 max-h-96 space-y-4 overflow-auto">
              <li v-for="credit in report.credits || []" :key="credit.id" class="rounded-lg bg-gray-50 p-4 dark:bg-dark-800">
                <div class="flex flex-wrap justify-between gap-2"><span class="break-all font-medium">{{ walletName(credit.wallet_key) }}</span><time class="text-sm text-gray-500">{{ businessTime(credit.observed_at) }}</time></div>
                <p class="mt-2"><strong class="text-emerald-600">+{{ businessMoney(credit.amount) }}</strong><span class="ml-3 text-sm text-gray-500">{{ credit.kind.startsWith('checkin') || credit.kind === 'gift' ? '疑似签到／赠送' : '疑似充值' }}{{ credit.confirmed ? ' · 当日金额已修正' : '' }}</span></p>
              </li>
              <li v-if="!report.credits?.length" class="py-8 text-center text-gray-500">此区间未捕捉到余额正向变动</li>
            </ul>
          </section>
        </div>

        <section class="card overflow-hidden">
          <div class="flex flex-wrap items-center justify-between gap-3 px-5 py-5"><h2 class="text-lg font-semibold">每日经营汇总</h2><button v-if="report.daily.length > 31" class="text-primary-600" @click="showAllDays = !showAllDays">{{ showAllDays ? '收起' : `查看全部 ${report.daily.length} 天` }}</button></div>
          <div class="overflow-x-auto"><table class="w-full whitespace-nowrap text-left">
            <thead class="bg-gray-50 text-sm text-gray-500 dark:bg-dark-800"><tr><th class="px-5 py-3">日期</th><th class="px-5 py-3">计入客户消费</th><th class="px-5 py-3">计入预计成本</th><th class="px-5 py-3">计入预计毛利</th><th class="px-5 py-3">钱包整体消耗</th><th class="px-5 py-3">覆盖情况</th></tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600"><tr v-for="day in visibleDays" :key="day.date">
              <td class="px-5 py-4"><button class="font-medium text-primary-600" @click="viewDay(day.date)">{{ day.date }}</button></td>
              <td class="px-5 py-4">{{ businessMoney(businessIncludedRevenue(day.revenue, day.excluded_media_revenue)) }}<p v-if="day.excluded_media_requests" class="mt-1 text-sm text-amber-700 dark:text-amber-400">暂不计入 {{ businessMoney(day.excluded_media_revenue) }}</p></td>
              <td class="px-5 py-4">{{ day.expected_cost == null ? '待确认／未计入' : businessMoney(day.expected_cost) }}</td>
              <td class="px-5 py-4" :class="(day.gross_profit ?? 0) < 0 ? 'text-red-600' : ''">{{ day.gross_profit == null ? '待确认／未计入' : businessMoney(day.gross_profit) }}</td>
              <td class="px-5 py-4">{{ businessMoney(day.balance_cost) }}</td><td class="px-5 py-4 text-sm text-gray-500">{{ day.partial ? '部分数据／估算' : '已采样' }}</td>
            </tr><tr v-if="!visibleDays.length"><td colspan="6" class="p-8 text-center text-gray-500">尚无每日汇总，历史用量正在按日整理。</td></tr></tbody>
          </table></div>
        </section>
        <p class="pb-4 text-sm leading-7 text-gray-500 dark:text-gray-400">预计成本＝本站记录的标准用量成本 × 有效上游探测倍率，不使用本地账号记账倍率，也不默认按1计算。待确认绘图/视频的收入与成本暂不计入预计毛利；其钱包整体余额消耗仍展示，但不做余额偏差及余额毛利核验。有有效探测倍率的绘图/视频仍正常计入。日内倍率变化见变动记录；历史补算采用补算时探测倍率，并非历史精确结算。本页不是逐笔账单核验。</p>
      </template>

      <div v-if="adjusting" class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" aria-labelledby="adjustment-title" @click.self="!saving && (adjusting = null)">
        <form class="card w-full max-w-lg space-y-5 p-6" @submit.prevent="saveAdjustment(false)">
          <h2 id="adjustment-title" class="text-xl font-semibold">修正当日净入账</h2>
          <p class="break-all text-gray-500">{{ adjusting.name }} · {{ date }}</p>
          <label class="block"><span class="mb-2 block font-medium">净入账总额</span><input v-model="adjustAmount" class="input" type="number" step="0.0001" min="-10000000" max="10000000" required /><span class="mt-2 block text-sm leading-6 text-gray-500">填写当天充值、赠送等净入账总额，替换自动推测金额；不是额外追加。无充值填0。</span></label>
          <label class="block"><span class="mb-2 block font-medium">备注</span><input v-model="adjustNote" class="input" maxlength="200" placeholder="例如：今日充值20元" /></label>
          <p v-if="adjustError" class="text-red-600" role="alert">{{ adjustError }}</p>
          <div class="flex flex-wrap justify-end gap-3"><button type="button" class="btn btn-secondary" :disabled="saving" @click="adjusting = null">取消</button><button type="button" class="btn btn-secondary" :disabled="saving" @click="saveAdjustment(true)">恢复自动推测</button><button class="btn btn-primary" :disabled="saving">{{ saving ? '保存中…' : '保存并重算' }}</button></div>
        </form>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { businessMonitorAPI, type BusinessPeriod, type BusinessReport, type BusinessWallet, type BusinessBalanceSnapshot } from '@/api/admin/businessMonitor'
import { businessMoney, businessTime, shanghaiDay, businessIncludedRevenue } from './businessMonitorFormatting'

const periods: { value: BusinessPeriod; label: string }[] = [{ value: 'day', label: '日' }, { value: 'week', label: '周' }, { value: 'month', label: '月' }, { value: 'year', label: '年' }]
const period = ref<BusinessPeriod>('day')
const today = shanghaiDay()
const date = ref(today)
const report = ref<BusinessReport | null>(null)
const balances = ref<BusinessBalanceSnapshot | null>(null)
const balanceLoading = ref(false)
const balanceError = ref('')
const loading = ref(false)
const refreshing = ref(false)
const error = ref('')
const showAllDays = ref(false)
const busy = computed(() => loading.value || refreshing.value)
const rangeLabel = computed(() => {
  if (!report.value) return ''
  const start = shanghaiDay(new Date(report.value.start))
  const end = shanghaiDay(new Date(new Date(report.value.end).getTime() - 1))
  return start === end ? start : `${start} 至 ${end}`
})
const metrics = computed(() => {
  const s = report.value?.summary
  if (!s) return []
  return [
    { label: '计入客户消费额', value: businessIncludedRevenue(s.revenue, s.excluded_media_revenue), help: '已排除待确认绘图/视频，原始总消费另列', negative: false },
    { label: '计入预计成本', value: s.expected_cost, help: s.expected_cost == null ? '其余用量仍有成本待确认，或全部用量已排除' : '标准用量成本 × 上游探测倍率；待确认绘图/视频不计入', negative: false },
    { label: '已覆盖钱包整体消耗', value: s.balance_cost, help: '包含被排除的绘图/视频消耗，不与计入成本直接比较', negative: false },
    { label: '计入预计毛利', value: s.gross_profit, help: s.gross_profit == null ? '其余成本未完整确认，或全部用量已排除' : '仅计入用量的消费减对应成本；套餐另列', negative: (s.gross_profit ?? 0) < 0 }
  ]
})
const visibleDays = computed(() => {
  const days = [...(report.value?.daily || [])].sort((a, b) => b.date.localeCompare(a.date))
  return showAllDays.value ? days : days.slice(0, 31)
})
function messageOf(e: unknown): string { return e && typeof e === 'object' && 'message' in e ? String(e.message) : '读取失败，请稍后重试' }
async function loadReport() {
  if (!date.value || busy.value) return
  loading.value = true; error.value = ''
  try { report.value = await businessMonitorAPI.getReport(period.value, date.value) }
  catch (e) { error.value = messageOf(e) }
  finally { loading.value = false }
}
async function selectPeriod(value: BusinessPeriod) { period.value = value; showAllDays.value = false; await loadReport() }
async function viewDay(value: string) { period.value = 'day'; date.value = value; await loadReport() }
async function refreshToday() {
  if (busy.value) return
  refreshing.value = true; error.value = ''
  try { const result = await businessMonitorAPI.refresh(); period.value = 'day'; date.value = shanghaiDay(); report.value = result; await loadBalances() }
  catch (e) { error.value = messageOf(e) }
  finally { refreshing.value = false }
}
function balanceStatus(status: string): string { return ({ ready: '正常', low: '余额偏低', error: '查询失败', waiting: '等待凭据' } as Record<string, string>)[status] || '待检查' }
function balanceMoney(currency: string, value: number | null): string {
  if (value == null || !Number.isFinite(value)) return '—'
  return `${currency || '$'}${value.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`
}
async function loadBalances() {
  if (balanceLoading.value) return
  balanceLoading.value = true; balanceError.value = ''
  try { balances.value = await businessMonitorAPI.getBalances() }
  catch (e) { balanceError.value = messageOf(e) }
  finally { balanceLoading.value = false }
}
function statusText(status: string): string { return ({ ok: '范围内', partial: '数据待完善', overspend: '消耗偏高', loss: '疑似亏损', uncovered: '未覆盖余额', excluded: '暂不计入' } as Record<string, string>)[status] || '待检查' }
function statusClass(status: string): string {
  if (status === 'ok') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
  if (status === 'loss' || status === 'overspend') return 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  return 'bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300'
}
function rateText(status: string): string { return ({ ok: '倍率探测有效', fresh: '倍率探测有效', stale: '倍率探测已过期', failed: '倍率探测失败', invalid: '倍率探测数据不完整', unsupported: '上游不支持倍率探测', missing: '尚无倍率探测结果', mixed: '部分倍率需核查', unknown: '倍率状态未知' } as Record<string, string>)[status] || '倍率待确认' }
function walletProbeRates(wallet: BusinessWallet): string[] {
  return [...new Set((wallet.cost_basis || []).map(b => `${b.account_name}${b.usage_kind === 'image' ? ' [绘图]' : b.usage_kind === 'video' ? ' [视频]' : ''}：${b.excluded ? '待确认，暂不计入' : b.probe_rate == null ? '待确认' : b.probe_rate.toFixed(4)}`))]
}
function walletName(key: string): string { return report.value?.wallets.find(w => w.key === key)?.name || key }
const adjusting = ref<BusinessWallet | null>(null)
const adjustAmount = ref<number | string>(0)
const adjustNote = ref('')
const saving = ref(false)
const adjustError = ref('')
function openAdjustment(wallet: BusinessWallet) { adjusting.value = wallet; adjustAmount.value = wallet.credit_override ?? wallet.credits; adjustNote.value = wallet.credit_note || ''; adjustError.value = '' }
async function saveAdjustment(clear: boolean) {
  if (!adjusting.value || saving.value) return
  const amount = clear ? null : Number(adjustAmount.value)
  if (!clear && (adjustAmount.value === '' || !Number.isFinite(amount))) { adjustError.value = '请填写有效金额'; return }
  saving.value = true; adjustError.value = ''
  try {
    await businessMonitorAPI.setCreditOverride(adjusting.value.key, date.value, amount, adjustNote.value)
    adjusting.value = null
    await loadReport()
  } catch (e) { adjustError.value = messageOf(e) }
  finally { saving.value = false }
}
onMounted(() => { void loadReport(); void loadBalances() })
</script>

<style scoped>
.business-monitor { font-size: 16px; }
th, td { vertical-align: top; }
</style>
