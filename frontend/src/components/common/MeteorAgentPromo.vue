<template>
  <a
    :href="meteorAgentUrl"
    target="_blank"
    rel="noopener noreferrer"
    class="group relative overflow-hidden transition-all duration-200"
    :class="variantClasses"
    :aria-label="labelText"
    :title="props.collapsed || props.compact ? labelText : undefined"
  >
    <span
      v-if="props.variant === 'sidebar' && props.collapsed"
      class="flex h-full w-full items-center justify-center"
    >
      <Icon name="download" size="sm" class="text-primary-600 dark:text-primary-300" />
    </span>
    <span
      v-else
      class="flex min-w-0 flex-1 items-center"
      :class="props.variant === 'header' || props.compact ? 'gap-2' : 'gap-3'"
    >
      <span
        class="flex shrink-0 items-center justify-center rounded-xl bg-primary-500/15 text-primary-600 dark:bg-primary-400/15 dark:text-primary-300"
        :class="props.variant === 'header' || props.compact ? 'h-7 w-7 rounded-lg' : 'h-10 w-10'"
      >
        <Icon name="download" :size="props.variant === 'header' || props.compact ? 'sm' : 'md'" />
      </span>
      <span class="min-w-0 flex-1">
        <span
          class="block truncate font-semibold text-gray-900 dark:text-white"
          :class="props.variant === 'header' || props.compact ? 'text-xs' : 'text-sm'"
        >
          {{ labelText }}
        </span>
        <span
          v-if="props.variant !== 'header' && !props.compact"
          class="mt-0.5 block text-xs leading-relaxed text-gray-600 dark:text-dark-300"
        >
          {{ t('home.meteorAgent.description') }}
        </span>
      </span>
      <Icon
        name="externalLink"
        size="sm"
        class="shrink-0 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-primary-500"
      />
    </span>
  </a>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

type PromoVariant = 'hero' | 'dashboard' | 'header' | 'sidebar'

const props = withDefaults(
  defineProps<{
    variant?: PromoVariant
    collapsed?: boolean
    compact?: boolean
  }>(),
  {
    variant: 'dashboard',
    collapsed: false,
    compact: false,
  },
)

const { t } = useI18n()
const meteorAgentUrl = 'https://dl.meteor21c.fun/'

const labelText = computed(() =>
  props.variant === 'header' || props.compact || (props.variant === 'sidebar' && props.collapsed)
    ? t('home.meteorAgent.short')
    : t('home.meteorAgent.title'),
)

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'hero':
      return 'flex w-full items-center rounded-2xl border border-primary-200/70 bg-gradient-to-r from-primary-50 via-white to-cyan-50 p-4 shadow-sm hover:-translate-y-0.5 hover:shadow-md dark:border-primary-800/60 dark:from-primary-950/50 dark:via-dark-900 dark:to-cyan-950/40'
    case 'header':
      return 'hidden h-10 items-center rounded-lg border border-primary-200/70 bg-primary-50/70 px-2.5 text-gray-700 hover:border-primary-300 hover:bg-primary-100 dark:border-primary-800/60 dark:bg-primary-950/30 dark:text-dark-100 dark:hover:border-primary-700 dark:hover:bg-primary-900/40 sm:flex'
    case 'sidebar':
      return props.collapsed
        ? 'flex h-10 w-full items-center justify-center rounded-xl border border-primary-200/70 bg-primary-50/70 text-primary-700 hover:border-primary-300 hover:bg-primary-100 dark:border-primary-800/60 dark:bg-primary-950/30 dark:text-primary-200 dark:hover:border-primary-700 dark:hover:bg-primary-900/40'
        : props.compact
          ? 'flex h-11 w-full min-w-0 items-center rounded-xl border border-primary-200/70 bg-primary-50/70 px-2 text-gray-700 hover:border-primary-300 hover:bg-primary-100 dark:border-primary-800/60 dark:bg-primary-950/30 dark:text-dark-100 dark:hover:border-primary-700 dark:hover:bg-primary-900/40'
        : 'flex w-full items-center rounded-xl border border-primary-200/70 bg-primary-50/70 p-2.5 text-gray-700 hover:border-primary-300 hover:bg-primary-100 dark:border-primary-800/60 dark:bg-primary-950/30 dark:text-dark-100 dark:hover:border-primary-700 dark:hover:bg-primary-900/40'
    case 'dashboard':
    default:
      return 'flex w-full items-center rounded-2xl border border-primary-200/70 bg-gradient-to-r from-primary-50 to-cyan-50 p-4 shadow-sm hover:border-primary-300 hover:shadow-md dark:border-primary-800/60 dark:from-primary-950/50 dark:to-cyan-950/40'
  }
})
</script>
