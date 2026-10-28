import { apiClient } from './client'

export const canvasPilotAPI = {
  async getAccess(): Promise<{ enabled: boolean; allowed: boolean }> {
    const { data } = await apiClient.get('/canvas/access')
    return data
  }
}
