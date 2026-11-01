export function businessMoney(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '—'
  return `¥${value.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`
}

export function businessIncludedRevenue(total: number, excluded = 0): number {
  const included = total - excluded
  return Math.abs(included) < 1e-9 ? 0 : included
}

export function shanghaiDay(now = new Date()): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit'
  }).formatToParts(now)
  const part = (type: string) => parts.find(p => p.type === type)?.value || ''
  return `${part('year')}-${part('month')}-${part('day')}`
}

export function businessTime(value: string | null | undefined): string {
  if (!value || !Number.isFinite(new Date(value).getTime())) return '尚无记录'
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hour12: false
  }).format(new Date(value))
}

export function estimateMoney(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? '暂无成本数据' : businessMoney(value)
}

export function businessCoverage(estimate: { revenue: number; covered_requests: number; uncovered_requests: number }, totalRevenue: number): string {
  const requests = estimate.covered_requests + estimate.uncovered_requests
  const ratio = totalRevenue > 0 ? estimate.revenue / totalRevenue : requests > 0 ? estimate.covered_requests / requests : 0
  const percent = Math.max(0, Math.min(100, ratio * 100))
  // Do not round a partial period up to apparently complete coverage.
  return `${percent < 100 && percent > 99.9 ? '99.9' : percent.toFixed(1)}%`
}
