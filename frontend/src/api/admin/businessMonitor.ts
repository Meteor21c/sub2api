import { apiClient } from '../client'

export type BusinessPeriod = 'day' | 'week' | 'month' | 'year'
export interface BusinessSummary {
  revenue: number
  subscription_usage: number
  excluded_media_revenue: number
  excluded_media_requests: number
  expected_cost: number | null
  gross_profit: number | null
  balance_cost: number | null
  observed_profit: number | null
  deviation: number | null
  anomaly_count: number
  partial: boolean
}
export interface BusinessWallet {
  key: string
  base_url: string
  name: string
  account_ids: number[]
  revenue: number
  subscription_usage: number
  excluded_media_revenue: number
  excluded_media_requests: number
  expected_cost: number | null
  gross_profit: number | null
  cost_version: number
  cost_basis: { account_id: number; account_name: string; standard_cost: number; probe_rate: number | null; rate_status: string; usage_kind: string; excluded: boolean }[]
  balance_start: number | null
  balance_end: number | null
  credits: number
  inferred_credits: number
  credit_override: number | null
  credit_note?: string
  balance_cost: number | null
  observed_profit: number | null
  deviation: number | null
  tolerance: number | null
  status: string
  messages: string[]
  last_sample_at: string | null
  rate_status: string
}
export interface BusinessDaily {
  date: string
  revenue: number
  subscription_usage: number
  excluded_media_revenue: number
  excluded_media_requests: number
  expected_cost: number | null
  gross_profit: number | null
  balance_cost: number | null
  observed_profit: number | null
  partial: boolean
}
export interface BusinessRateChange {
  id: number
  account_id: number
  account_name: string
  kind: string
  old_rate: number
  new_rate: number
  observed_at: string
}
export interface BusinessCredit {
  id: number
  wallet_key: string
  amount: number
  kind: string
  observed_at: string
  confirmed: boolean
}
export interface BusinessReport {
  period: BusinessPeriod
  date: string
  start: string
  end: string
  as_of: string
  finalized: boolean
  coverage_start: string | null
  summary: BusinessSummary
  wallets: BusinessWallet[]
  daily: BusinessDaily[]
  rate_changes: BusinessRateChange[]
  credits: BusinessCredit[]
}

export interface BusinessBalanceSnapshot {
  generatedAt: string
  accounts: {
    id: string
    name: string
    baseUrl: string
    balance: number | null
    status: string
    currency: string
    planName: string
    threshold: number
  }[]
}

export const businessMonitorAPI = {
  async getBalances(): Promise<BusinessBalanceSnapshot> {
    return (await apiClient.get('/admin/business-monitor/balances', { timeout: 30000 })).data
  },
  async getAccess(): Promise<{ enabled: boolean; allowed: boolean }> {
    return (await apiClient.get('/admin/business-monitor/access')).data
  },
  async getReport(period: BusinessPeriod, date: string): Promise<BusinessReport> {
    return (await apiClient.get('/admin/business-monitor/report', { params: { period, date } })).data
  },
  async refresh(): Promise<BusinessReport> {
    return (await apiClient.post('/admin/business-monitor/refresh', {}, { timeout: 60000 })).data
  },
  async setCreditOverride(wallet_key: string, date: string, credit_amount: number | null, note: string): Promise<void> {
    await apiClient.put('/admin/business-monitor/credit-override', { wallet_key, date, credit_amount, note })
  }
}
export default businessMonitorAPI
