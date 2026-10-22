import { describe, expect, it } from 'vitest'
import { smsInfo } from './sms'

// Segments are information only: the limit is 160 characters (D2).
describe('smsInfo', () => {
  it('fits 160 GSM-7 characters in one segment', () => {
    expect(smsInfo('a'.repeat(160))).toEqual({ encoding: 'GSM-7', units: 160, segments: 1 })
  })

  it('splits longer GSM-7 text into 153-character parts', () => {
    expect(smsInfo('a'.repeat(161)).segments).toBe(2)
    expect(smsInfo('a'.repeat(306)).segments).toBe(2)
    expect(smsInfo('a'.repeat(307)).segments).toBe(3)
  })

  it('counts GSM-7 extension characters twice', () => {
    expect(smsInfo('€{}[]~^|\\')).toMatchObject({ encoding: 'GSM-7', units: 18 })
  })

  it('switches to UCS-2 for Turkish ş, ğ, ı and İ', () => {
    for (const c of ['ş', 'ğ', 'ı', 'İ']) expect(smsInfo(`Merhaba ${c}`).encoding).toBe('UCS-2')
  })

  it('keeps GSM-7 for characters the alphabet has (ç is not one of them, Ç and ü are)', () => {
    expect(smsInfo('Çü öÖ ñ é').encoding).toBe('GSM-7')
    expect(smsInfo('ç').encoding).toBe('UCS-2')
  })

  it('fits 70 UCS-2 characters in one segment, 67 per part after that', () => {
    expect(smsInfo('ş'.repeat(70))).toEqual({ encoding: 'UCS-2', units: 70, segments: 1 })
    expect(smsInfo('ş'.repeat(71)).segments).toBe(2)
    expect(smsInfo('ş'.repeat(134)).segments).toBe(2)
    expect(smsInfo('ş'.repeat(135)).segments).toBe(3)
  })

  it('counts an emoji as two UCS-2 units', () => {
    expect(smsInfo('😀')).toEqual({ encoding: 'UCS-2', units: 2, segments: 1 })
  })

  it('is empty for no text', () => {
    expect(smsInfo('')).toEqual({ encoding: 'GSM-7', units: 0, segments: 0 })
  })
})
