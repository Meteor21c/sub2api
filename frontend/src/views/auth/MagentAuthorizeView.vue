<template>
  <main class="min-h-screen bg-gray-50 px-4 py-12 dark:bg-dark-950">
    <section class="card mx-auto max-w-md space-y-4 p-6 sm:p-8" aria-labelledby="magent-signin-title">
      <h1 id="magent-signin-title" class="text-xl font-semibold text-gray-900 dark:text-white">
        {{ t('auth.magent.title') }}
      </h1>
      <p v-if="state === 'expired' || !requestID" role="alert" class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('auth.magent.expired') }}
      </p>
      <p v-else-if="state === 'approved' || state === 'denied'" role="status" class="text-sm text-gray-600 dark:text-gray-300">
        {{ t(state === 'approved' ? 'auth.magent.approved' : 'auth.magent.denied') }}
      </p>
      <form v-else class="space-y-4" @submit.prevent="decide(true)">
        <p v-if="auth.user?.email" class="break-all text-sm text-gray-600 dark:text-gray-300">
          {{ t('auth.magent.account', { email: auth.user.email }) }}
        </p>
        <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
          <p class="font-mono text-lg font-semibold tracking-wider text-gray-900 dark:text-white">{{ code }}</p>
          <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ t('auth.magent.compareCode') }}</p>
        </div>
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('auth.magent.access') }}</p>
        <p v-if="failed" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('auth.magent.failed') }}</p>
        <div class="flex flex-wrap justify-end gap-2">
          <button type="button" class="btn btn-secondary" :disabled="busy" @click="decide(false)">
            {{ t('auth.magent.cancel') }}
          </button>
          <button type="submit" class="btn btn-primary" :disabled="busy">
            {{ busy ? t('common.processing') : t('auth.magent.approve') }}
          </button>
        </div>
      </form>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const route = useRoute()
const auth = useAuthStore()
const requestID = computed(() => {
  const value = route.query.request
  return typeof value === 'string' && /^[a-f0-9]{32}$/.test(value) ? value : ''
})
const code = computed(() => `${requestID.value.slice(0, 4)}-${requestID.value.slice(4, 8)}`.toUpperCase())
const busy = ref(false)
const failed = ref(false)
const state = ref<'pending' | 'approved' | 'denied' | 'expired'>('pending')

async function decide(approved: boolean) {
  const id = requestID.value
  if (!id || busy.value || state.value !== 'pending') return
  busy.value = true
  failed.value = false
  try {
    await apiClient.post('/auth/magent/decision', { request_id: id, approved })
    if (requestID.value === id) state.value = approved ? 'approved' : 'denied'
  } catch (error) {
    if (requestID.value !== id) return
    if ((error as { status?: number } | null)?.status === 404) state.value = 'expired'
    else failed.value = true
  } finally {
    busy.value = false
  }
}
</script>
