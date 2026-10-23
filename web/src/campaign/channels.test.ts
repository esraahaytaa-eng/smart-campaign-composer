import { describe, expect, it } from 'vitest'
import { channelsLosingContent } from './channels'

// Removing a channel deletes its content (D38), so the composer asks first.
describe('channelsLosingContent', () => {
  it('lists removed channels that have content', () => {
    expect(channelsLosingContent(['sms', 'email'], ['sms'], { sms: { body: 'x' }, email: { subject: 's', body: 'b' } })).toEqual(['email'])
  })

  it('is empty when the removed channel has no content yet', () => {
    expect(channelsLosingContent(['sms', 'email'], ['sms'], { sms: { body: 'x' } })).toEqual([])
    expect(channelsLosingContent(['sms', 'email'], ['email'], null)).toEqual([])
  })

  it('is empty when channels are only added', () => {
    expect(channelsLosingContent(['sms'], ['sms', 'email'], { sms: { body: 'x' } })).toEqual([])
  })
})
