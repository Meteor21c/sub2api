<template>
  <div class="space-y-3" data-testid="availability-quick-result">
    <p v-if="turn.status === 'running'" class="text-xs text-gray-500 dark:text-gray-400" role="status">
      {{ t('admin.channelTest.quickTestWaiting') }}
    </p>
    <p v-else-if="turn.status !== 'succeeded'" class="text-xs text-amber-700 dark:text-amber-300">
      {{ t('admin.channelTest.quickTestIncomplete') }}
    </p>

    <template v-else-if="testKind === 'pelican'">
      <div v-if="svgPreview?.status === 'ready' && svgPreview.src && !previewFailed" class="space-y-2">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="badge badge-success">{{ t('admin.channelTest.pelicanPreview') }}</span>
          <div class="flex items-center gap-3 text-xs">
            <button type="button" class="text-indigo-600 hover:underline dark:text-indigo-300" data-testid="availability-svg-expand" @click="expanded = true">
              {{ t('admin.channelTest.expandPreview') }}
            </button>
            <a :href="svgPreview.src" download="pelican.svg" class="text-indigo-600 hover:underline dark:text-indigo-300">
              {{ t('admin.channelTest.downloadSvg') }}
            </a>
          </div>
        </div>
        <img
          :src="svgPreview.src"
          :alt="t('admin.channelTest.pelicanPreviewAlt')"
          class="max-h-[28rem] w-full rounded-xl border border-gray-200 bg-white object-contain dark:border-dark-700"
          data-testid="availability-svg-preview"
          @error="handlePreviewError"
        />
        <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.pelicanPreviewHint') }}</p>
      </div>
      <p v-else class="text-xs text-amber-700 dark:text-amber-300" data-testid="availability-svg-unavailable">
        {{ svgPreview?.status === 'missing' ? t('admin.channelTest.svgMissing') : t('admin.channelTest.svgInvalid') }}
      </p>
    </template>

    <div v-else-if="testKind === 'candy' && candyResult" class="space-y-2" data-testid="availability-candy-result">
      <div class="flex flex-wrap items-center gap-2">
        <span class="badge" :class="candyResult.status === 'correct' ? 'badge-success' : candyResult.status === 'incorrect' ? 'badge-danger' : 'badge-gray'" data-testid="availability-candy-verdict">
          {{ candyResult.status === 'correct' ? t('admin.channelTest.candyCorrect') : candyResult.status === 'incorrect' ? t('admin.channelTest.candyIncorrect') : t('admin.channelTest.candyUngraded') }}
        </span>
        <span class="text-sm">{{ t('admin.channelTest.candyModelAnswer') }}: <strong>{{ candyResult.answer ?? '—' }}</strong></span>
        <span class="text-sm">{{ t('admin.channelTest.candyExpectedAnswer') }}: <strong>{{ CANDY_EXPECTED_ANSWER }}</strong></span>
      </div>
      <p v-if="candyResult.status === 'ungraded'" class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.channelTest.candyUngradedHint') }}</p>
      <p class="text-xs leading-5 text-gray-600 dark:text-gray-300">{{ t('admin.channelTest.candyExplanation') }}</p>
      <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.channelTest.candyVerdictHint') }}</p>
    </div>

    <details v-if="turn.content" :open="testKind === 'candy'" class="rounded-xl border border-gray-200 px-3 py-2 dark:border-dark-700" data-testid="availability-quick-raw">
      <summary class="cursor-pointer text-xs font-medium text-gray-600 dark:text-gray-300">{{ t('admin.channelTest.rawTestResponse') }}</summary>
      <div class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words text-sm leading-6">{{ turn.content }}</div>
    </details>

    <BaseDialog :show="expanded && !previewFailed" :title="t('admin.channelTest.pelicanPreview')" width="extra-wide" @close="expanded = false">
      <img v-if="svgPreview?.src" :src="svgPreview.src" :alt="t('admin.channelTest.pelicanPreviewAlt')" class="max-h-[75vh] w-full rounded-xl bg-white object-contain" @error="handlePreviewError" />
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AvailabilityTurn } from '@/api/admin/availability'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { CANDY_EXPECTED_ANSWER, evaluateCandyAnswer, getQuickTestPreset, prepareSvgPreview } from '@/utils/availabilityQuickTests'

const props = defineProps<{ turn: Pick<AvailabilityTurn, 'prompt' | 'content' | 'status'> }>()
const { t } = useI18n()
const expanded = ref(false)
const previewFailed = ref(false)
const testKind = computed(() => getQuickTestPreset(props.turn.prompt))
const svgPreview = computed(() => testKind.value === 'pelican' && props.turn.status === 'succeeded' ? prepareSvgPreview(props.turn.content) : null)
const candyResult = computed(() => testKind.value === 'candy' && props.turn.status === 'succeeded' ? evaluateCandyAnswer(props.turn.content) : null)

watch(() => svgPreview.value?.src, () => {
  previewFailed.value = false
  expanded.value = false
})

function handlePreviewError(): void {
  previewFailed.value = true
  expanded.value = false
}
</script>
