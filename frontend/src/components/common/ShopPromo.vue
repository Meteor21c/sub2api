<template>
  <a
    :href="shopUrl"
    target="_blank"
    rel="sponsored noopener noreferrer"
    class="group relative flex w-full min-w-0 items-center rounded-2xl border p-4 shadow-sm transition-all duration-200 hover:-translate-y-0.5 hover:shadow-md"
    :class="variantClasses"
    :aria-label="t('home.shop.title')"
  >
    <span class="flex min-w-0 items-center gap-3">
      <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-amber-500/15 text-amber-600 dark:bg-amber-400/15 dark:text-amber-300">
        <Icon name="gift" size="md" />
      </span>
      <span class="min-w-0 flex-1">
        <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('home.shop.title') }}
        </span>
        <span class="mt-0.5 block text-xs leading-relaxed text-gray-600 dark:text-dark-300">
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

type PromoVariant = 'hero'

const props = withDefaults(
  defineProps<{
    variant?: PromoVariant
  }>(),
  {
    variant: 'hero',
  },
)

const { t } = useI18n()
const shopUrl = 'https://www.16688.com.cn/shop/S256888'

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'hero':
    default:
      return 'border-amber-200/70 bg-gradient-to-r from-amber-50 via-white to-orange-50 dark:border-amber-800/60 dark:from-amber-950/50 dark:via-dark-900 dark:to-orange-950/40'
  }
})
</script>
