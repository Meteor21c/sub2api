/** The retired balance dashboard now lives in the owner-only native admin page. */
export function isLegacyBalanceMonitorMenu(item: { id: string; url?: string }): boolean {
  if (item.id !== 'meteor-monitor' || !item.url) return false
  try {
    const url = new URL(item.url)
    return (
      (url.protocol === 'https:' || url.protocol === 'http:') &&
      url.hostname === 'monitor.meteor21c.fun' &&
      !url.port &&
      !url.username &&
      !url.password &&
      url.pathname === '/'
    )
  } catch {
    return false
  }
}
