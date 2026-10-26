<template>
  <a
    :href="shopUrl"
    target="_blank"
    rel="sponsored noopener noreferrer"
    class="group relative overflow-hidden transition-all duration-200"
    :class="variantClasses"
    :aria-label="labelText"
    :title="props.variant !== 'hero' ? labelText : undefined"
  >
    <span
      v-if="props.variant === 'sidebar' && props.collapsed"
      class="flex h-full w-full items-center justify-center"
    >
      <Icon name="gift" size="sm" class="text-amber-600 dark:text-amber-300" />
    </span>
    <span
      v-else
      class="flex min-w-0 flex-1 items-center"
      :class="props.variant === 'hero' ? 'gap-3' : 'gap-2'"
    >
      <span
        class="flex shrink-0 items-center justify-center bg-amber-500/15 text-amber-600 dark:bg-amber-400/15 dark:text-amber-300"
        :class="props.variant === 'hero' ? 'h-10 w-10 rounded-xl' : 'h-7 w-7 rounded-lg'"
      >
        <Icon name="gift" :size="props.variant === 'hero' ? 'md' : 'sm'" />
      </span>
      <span class="min-w-0 flex-1">
        <span
          class="block truncate font-semibold text-gray-900 dark:text-white"
          :class="props.variant === 'hero' ? 'text-sm' : 'text-xs'"
        >
          {{ labelText }}
        </span>
        <span
          v-if="props.variant === 'hero'"
          class="mt-0.5 block text-xs leading-relaxed text-gray-600 dark:text-dark-300"
        >
          {{ t('home.shop.description') }}
        </span>
      </span>
      <Icon
        name="externalLink"
        size="sm"
        class="shrink-0 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-amber-500"
      />
    </span>
  </a>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

type PromoVariant = 'hero' | 'header' | 'sidebar'

const props = withDefaults(
  defineProps<{
    variant?: PromoVariant
    collapsed?: boolean
  }>(),
  {
    variant: 'hero',
    collapsed: false,
  },
)

const { t } = useI18n()
const shopUrl = 'https://www.16688.com.cn/shop/S256888'
const labelText = computed(() =>
  props.variant === 'hero' ? t('home.shop.title') : t('home.shop.short'),
)

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'header':
      return 'hidden h-10 items-center rounded-lg border border-amber-200/70 bg-amber-50/70 px-2.5 text-gray-700 hover:border-amber-300 hover:bg-amber-100 dark:border-amber-800/60 dark:bg-amber-950/30 dark:text-dark-100 dark:hover:border-amber-700 dark:hover:bg-amber-900/40 sm:flex'
    case 'sidebar':
      return props.collapsed
        ? 'flex h-10 w-full items-center justify-center rounded-xl border border-amber-200/70 bg-amber-50/70 text-amber-700 hover:border-amber-300 hover:bg-amber-100 dark:border-amber-800/60 dark:bg-amber-950/30 dark:text-amber-200 dark:hover:border-amber-700 dark:hover:bg-amber-900/40'
        : 'flex h-11 w-full min-w-0 items-center rounded-xl border border-amber-200/70 bg-amber-50/70 px-2 text-gray-700 hover:border-amber-300 hover:bg-amber-100 dark:border-amber-800/60 dark:bg-amber-950/30 dark:text-dark-100 dark:hover:border-amber-700 dark:hover:bg-amber-900/40'
    case 'hero':
    default:
      return 'flex w-full min-w-0 items-center rounded-2xl border border-amber-200/70 bg-gradient-to-r from-amber-50 via-white to-orange-50 p-4 shadow-sm hover:-translate-y-0.5 hover:shadow-md dark:border-amber-800/60 dark:from-amber-950/50 dark:via-dark-900 dark:to-orange-950/40'
  }
})
</script>
