import { describe, expect, it } from 'vitest'
import { channelsKeepingText } from './channels'

// D67: a removed channel's text is kept and comes back with the channel.
describe('channelsKeepingText', () => {
  it('lists removed channels that have text in any language', () => {
    expect(channelsKeepingText(['sms', 'email'], ['sms'], { tr: { sms: { body: 'x' } }, en: { email: { subject: 's', body: 'b' } } })).toEqual(['email'])
  })

  it('is empty when the removed channel has no text yet', () => {
    expect(channelsKeepingText(['sms', 'email'], ['sms'], { tr: { sms: { body: 'x' } } })).toEqual([])
    expect(channelsKeepingText(['sms', 'email'], ['email'], null)).toEqual([])
  })

  it('is empty when channels are only added', () => {
    expect(channelsKeepingText(['sms'], ['sms', 'email'], { tr: { sms: { body: 'x' } } })).toEqual([])
  })
})
