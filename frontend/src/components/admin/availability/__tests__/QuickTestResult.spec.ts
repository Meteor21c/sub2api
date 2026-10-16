import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'

import type { AvailabilityTurn } from '@/api/admin/availability'
import { QUICK_TEST_PROMPTS } from '@/utils/availabilityQuickTests'
import QuickTestResult from '../QuickTestResult.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

type ResultTurn = Pick<AvailabilityTurn, 'prompt' | 'content' | 'status'>
const pelicanSvg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 60"><title>鹈鹕</title><circle cx="30" cy="30" r="10" /></svg>'

function mountResult(turn: Partial<ResultTurn> = {}) {
  return mount(QuickTestResult, {
    props: {
      turn: { prompt: QUICK_TEST_PROMPTS.pelican, content: pelicanSvg, status: 'succeeded', ...turn }
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          emits: ['close'],
          template: '<div v-if="show" data-testid="preview-dialog"><button data-testid="preview-dialog-close" @click="$emit(\'close\')">Close</button><slot /></div>'
        }
      }
    }
  })
}

describe('availability quick test result', () => {
  it('previews completed SVG output with matching download and expandable image', async () => {
    const wrapper = mountResult()
    const preview = wrapper.get('[data-testid="availability-svg-preview"]')
    const source = preview.attributes('src')

    expect(source).toMatch(/^data:image\/svg\+xml;charset=utf-8,/)
    expect(decodeURIComponent(source.split(',').slice(1).join(','))).toContain('鹈鹕')
    expect(wrapper.get('a[download="pelican.svg"]').attributes('href')).toBe(source)
    expect(wrapper.find('[data-testid="preview-dialog"]').exists()).toBe(false)

    await wrapper.get('[data-testid="availability-svg-expand"]').trigger('click')
    expect(wrapper.get('[data-testid="preview-dialog"] img').attributes('src')).toBe(source)
    await wrapper.get('[data-testid="preview-dialog-close"]').trigger('click')
    expect(wrapper.find('[data-testid="preview-dialog"]').exists()).toBe(false)
  })

  it('retains the complete model output as escaped text rather than executable HTML', () => {
    const content = `${pelicanSvg}\n<script>alert('raw')</script><img src=x onerror="alert('raw')">`
    const wrapper = mountResult({ content })
    const raw = wrapper.get('[data-testid="availability-quick-raw"]')

    expect(raw.text()).toContain(content)
    expect(raw.html()).toContain('&lt;script&gt;')
    expect(raw.find('svg, script, img').exists()).toBe(false)
    expect(raw.attributes('open')).toBeUndefined()
  })

  it.each(['running', 'failed', 'cancelled'] as const)('does not grade candy output for a %s turn', status => {
    const wrapper = mountResult({ prompt: QUICK_TEST_PROMPTS.candy, content: 'FINAL_ANSWER: 21', status })

    expect(wrapper.find('[data-testid="availability-candy-result"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="availability-candy-verdict"]').exists()).toBe(false)
    expect(wrapper.text()).toContain(status === 'running' ? 'admin.channelTest.quickTestWaiting' : 'admin.channelTest.quickTestIncomplete')
    expect(wrapper.get('[data-testid="availability-quick-raw"]').text()).toContain('FINAL_ANSWER: 21')
  })

  it.each([
    { content: 'A short solution.\nFINAL_ANSWER: 21', verdict: 'candyCorrect', answer: '21', badge: 'badge-success' },
    { content: 'The reasoning mentions 21, but the answer is different.\nFINAL_ANSWER: 29', verdict: 'candyIncorrect', answer: '29', badge: 'badge-danger' },
    { content: 'There are 21 candies.', verdict: 'candyUngraded', answer: '—', badge: 'badge-gray' }
  ])('shows $verdict for completed candy output', ({ content, verdict, answer, badge }) => {
    const wrapper = mountResult({ prompt: QUICK_TEST_PROMPTS.candy, content })
    const result = wrapper.get('[data-testid="availability-candy-result"]')
    const resultBadge = result.get('[data-testid="availability-candy-verdict"]')

    expect(resultBadge.text()).toBe(`admin.channelTest.${verdict}`)
    expect(resultBadge.classes()).toContain(badge)
    expect(result.findAll('strong').map(element => element.text())).toEqual([answer, '21'])
    expect(wrapper.get('[data-testid="availability-quick-raw"]').attributes('open')).toBeDefined()
  })

  it('recognizes restored tests from their own saved prompt and ignores answer-like custom messages', async () => {
    const wrapper = mountResult()
    expect(wrapper.find('[data-testid="availability-svg-preview"]').exists()).toBe(true)

    await wrapper.setProps({
      turn: { prompt: `\n${QUICK_TEST_PROMPTS.candy}\n`, content: 'FINAL_ANSWER: 21', status: 'succeeded' }
    })
    expect(wrapper.find('[data-testid="availability-svg-preview"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="availability-candy-verdict"]').text()).toBe('admin.channelTest.candyCorrect')

    await wrapper.setProps({
      turn: { prompt: 'A custom historical message', content: `${pelicanSvg}\nFINAL_ANSWER: 21`, status: 'succeeded' }
    })
    expect(wrapper.find('[data-testid="availability-svg-preview"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="availability-candy-result"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="availability-quick-raw"]').text()).toContain('FINAL_ANSWER: 21')
  })

  it('falls back to raw output after image errors and recovers when a different SVG arrives', async () => {
    const wrapper = mountResult()
    await wrapper.get('[data-testid="availability-svg-expand"]').trigger('click')
    await wrapper.get('[data-testid="preview-dialog"] img').trigger('error')

    expect(wrapper.find('[data-testid="availability-svg-preview"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="preview-dialog"]').exists()).toBe(false)
    expect(wrapper.find('a[download]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="availability-svg-unavailable"]').text()).toBe('admin.channelTest.svgInvalid')
    expect(wrapper.get('[data-testid="availability-quick-raw"]').text()).toContain(pelicanSvg)

    await wrapper.setProps({
      turn: { prompt: QUICK_TEST_PROMPTS.pelican, content: pelicanSvg.replace('cx="30"', 'cx="40"'), status: 'succeeded' }
    })
    expect(wrapper.find('[data-testid="availability-svg-preview"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="availability-svg-unavailable"]').exists()).toBe(false)
  })
})
