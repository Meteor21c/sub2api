import { describe, expect, it } from 'vitest'
import { businessMoney, businessTime, shanghaiDay } from '../businessMonitorFormatting'

describe('经营监测数据口径', () => {
  it('没有采到余额时不显示为零，真实零余额保留', () => {
    expect(businessMoney(null)).toBe('—')
    expect(businessMoney(undefined)).toBe('—')
    expect(businessMoney(Number.NaN)).toBe('—')
    expect(businessMoney(0)).toBe('¥0.00')
  })
  it('不把负毛利显示成正数，保留小额签到精度', () => {
    expect(businessMoney(-0.035)).toBe('¥-0.035')
    expect(businessMoney(0.03)).toBe('¥0.03')
  })
  it('日切与客户端时区无关，固定使用北京时间', () => {
    expect(shanghaiDay(new Date('2026-10-27T15:59:59Z'))).toBe('2026-10-27')
    expect(shanghaiDay(new Date('2026-10-27T16:00:00Z'))).toBe('2026-10-28')
    expect(shanghaiDay(new Date('2026-12-31T16:00:00Z'))).toBe('2027-01-01')
  })
  it('无效采样时间不能伪装成当前时间', () => {
    expect(businessTime(null)).toBe('尚无记录')
    expect(businessTime('not-a-date')).toBe('尚无记录')
  })
})
