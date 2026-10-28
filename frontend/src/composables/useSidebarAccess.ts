import { ref, watch } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { canvasPilotAPI } from '@/api/canvasPilot'
import { businessMonitorAPI } from '@/api/admin/businessMonitor'

const CACHE_KEY = 'sidebar_access_v1'
const CACHE_TTL = 24 * 60 * 60 * 1000
type Access = { enabled: boolean; allowed: boolean }
type CachedAccess = {
  scope: string
  checkedAt: number
  studio: boolean
  business: boolean
}

function readCache(scope: string): CachedAccess | null {
  try {
    const raw = JSON.parse(localStorage.getItem(CACHE_KEY) || 'null')
    const age = Date.now() - raw?.checkedAt
    if (raw?.scope === scope && Number.isFinite(age) && age >= 0 && age < CACHE_TTL &&
        typeof raw.studio === 'boolean' && typeof raw.business === 'boolean') return raw
  } catch {
    // Navigation still works when browser storage is unavailable.
  }
  return null
}

function explicitlyDenied(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  return [401, 403, 404].includes(Number((error as { status?: number }).status))
}

export function useSidebarAccess() {
  const authStore = useAuthStore()
  const businessMonitorAllowed = ref(false)
  const canvasPilotAllowed = ref(false)

  watch(
    () => {
      const user = authStore.user
      return user && user.id > 0 && (user.role === 'user' || user.role === 'admin')
        ? user.id + ':' + user.role
        : ''
    },
    (scope, _previous, onCleanup) => {
      let active = true
      onCleanup(() => { active = false })
      const admin = scope.endsWith(':admin')
      const cached = scope ? readCache(scope) : null
      canvasPilotAllowed.value = cached?.studio ?? false
      businessMonitorAllowed.value = admin && (cached?.business ?? false)
      if (!scope) return

      // Cache controls menu presentation only; every data API authorizes on the server.
      const persist = () => {
        try {
          localStorage.setItem(CACHE_KEY, JSON.stringify({
            scope, checkedAt: Date.now(),
            studio: canvasPilotAllowed.value,
            business: businessMonitorAllowed.value
          } satisfies CachedAccess))
        } catch {
          // A storage failure must not hide a successfully verified menu.
        }
      }
      const refresh = async (target: typeof canvasPilotAllowed, load: () => Promise<Access>) => {
        try {
          const access = await load()
          if (!active) return
          target.value = access.enabled === true && access.allowed === true
          persist()
        } catch (error) {
          if (!active || !explicitlyDenied(error)) return
          target.value = false
          persist()
        }
      }
      void refresh(canvasPilotAllowed, () => canvasPilotAPI.getAccess())
      if (admin) void refresh(businessMonitorAllowed, () => businessMonitorAPI.getAccess())
    },
    { immediate: true, flush: 'sync' }
  )

  return { businessMonitorAllowed, canvasPilotAllowed }
}
