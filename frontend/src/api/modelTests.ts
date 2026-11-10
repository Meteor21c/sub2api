import { apiClient } from './client'

export type ModelTestKind = 'pelican' | 'candy'
export type ModelTestSource = 'scheduled' | 'user'
export interface ModelTestPlan {
  id: number
  label: string
  group_id: number
  model: string
  test_kind: ModelTestKind
  enabled: boolean
  next_run_at: string | null
  last_run_at: string | null
}
export type ModelTestPlanInput = Pick<ModelTestPlan, 'label' | 'group_id' | 'model' | 'test_kind' | 'enabled'>
export interface ModelTestJob {
  id: string
  source: ModelTestSource
  api_key_id?: number
  plan_id?: number
  label: string
  model: string
  test_kind: ModelTestKind
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'interrupted'
  verdict: 'correct' | 'incorrect' | 'ungraded'
  answer: number | null
  content?: string
  error_code?: string
  total_ms: number
  input_tokens: number
  output_tokens: number
  created_at: string
  started_at: string | null
  completed_at: string | null
  expires_at: string
}
export interface ModelTestOverview {
  plans: ModelTestPlan[]
  jobs: ModelTestJob[]
  interval_hours: number
  retention_hours: number
}
export interface ModelTestKey { id: number; name: string; group_name: string }
export interface ModelTestGroup { id: number; name: string }

const base = '/model-tests'
const admin = '/admin/model-tests'
export const modelTestsAPI = {
  async overview(): Promise<ModelTestOverview> { return (await apiClient.get<ModelTestOverview>(`${base}/overview`)).data },
  async keys(): Promise<ModelTestKey[]> { return (await apiClient.get<ModelTestKey[]>(`${base}/keys`)).data },
  async models(keyId: number): Promise<string[]> { return (await apiClient.get<string[]>(`${base}/models`, { params: { api_key_id: keyId } })).data },
  async mine(): Promise<ModelTestJob[]> { return (await apiClient.get<ModelTestJob[]>(`${base}/jobs`)).data },
  async detail(id: string, source: ModelTestSource): Promise<ModelTestJob> {
    return (await apiClient.get<ModelTestJob>(`${base}/jobs/${id}`, { params: { source } })).data
  },
  async submit(input: { api_key_id: number; model: string; test_kind: ModelTestKind; client_request_id: string }): Promise<ModelTestJob> {
    return (await apiClient.post<ModelTestJob>(`${base}/jobs`, input)).data
  },
  async groups(): Promise<ModelTestGroup[]> { return (await apiClient.get<ModelTestGroup[]>(`${admin}/groups`)).data },
  async savePlan(id: number | null, input: ModelTestPlanInput): Promise<ModelTestPlan> {
    return (id ? await apiClient.put<ModelTestPlan>(`${admin}/plans/${id}`, input) : await apiClient.post<ModelTestPlan>(`${admin}/plans`, input)).data
  },
  async deletePlan(id: number): Promise<void> { await apiClient.delete(`${admin}/plans/${id}`) }
}
