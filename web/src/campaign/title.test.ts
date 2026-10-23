import { describe, expect, it } from 'vitest'
import { briefTitle } from './title'

// Campaigns are identified by their brief (D43): lists show its start.
describe('briefTitle', () => {
  it.each([
    ['Send a weekend sale announcement to all users in Ankara.', 'Send a weekend sale announcement to all users in Ankara.'],
    [
      'Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days.',
      'Send a 30% discount reminder to users in Istanbul who added…',
    ],
    ['Toplamda en az 10.000 TL harcayan müşterilere VIP teşekkür hediyesi gönder.', 'Toplamda en az 10.000 TL harcayan müşterilere VIP teşekkür…'],
    ['  Win back customers\n\nwho   left  ', 'Win back customers who left'],
    ['ş'.repeat(70), 'ş'.repeat(59) + '…'],
  ])('%j → %j', (brief, title) => {
    expect(briefTitle(brief)).toBe(title)
  })

  it('never exceeds 60 characters', () => {
    expect([...briefTitle('word '.repeat(40))].length).toBeLessThanOrEqual(60)
  })
})
