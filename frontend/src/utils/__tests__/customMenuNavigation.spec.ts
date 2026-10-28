import { describe, expect, it } from 'vitest'
import { isLegacyBalanceMonitorMenu } from '../customMenuNavigation'

describe('legacy balance monitor navigation', () => {
  it.each([
    'https://monitor.meteor21c.fun/',
    'https://monitor.meteor21c.fun',
    'http://monitor.meteor21c.fun/?embedded=1',
  ])('recognizes the retired monitor root %s', (url) => {
    expect(isLegacyBalanceMonitorMenu({ id: 'meteor-monitor', url })).toBe(true)
  })

  it.each([
    { id: 'other', url: 'https://monitor.meteor21c.fun/' },
    { id: 'meteor-monitor', url: 'https://example.com/' },
    { id: 'meteor-monitor', url: 'https://monitor.meteor21c.fun.evil.example/' },
    { id: 'meteor-monitor', url: 'https://monitor.meteor21c.fun/other-page' },
    { id: 'meteor-monitor', url: 'https://monitor.meteor21c.fun:8443/' },
    { id: 'meteor-monitor', url: 'https://user:pass@monitor.meteor21c.fun/' },
    { id: 'meteor-monitor', url: 'md:guide' },
    { id: 'meteor-monitor', url: '' },
  ])('keeps unrelated menu destinations intact: $url', (item) => {
    expect(isLegacyBalanceMonitorMenu(item)).toBe(false)
  })
})
