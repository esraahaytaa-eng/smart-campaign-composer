import { describe, expect, it } from 'vitest'
import { audienceByLanguage, languageName, orderLanguages, startingDefault, toWrite } from './languages'

const catalog = [
  { code: 'tr', name: 'Türkçe' },
  { code: 'en', name: 'English' },
]

describe('languageName', () => {
  it('uses the catalog name', () => {
    expect(languageName('en', catalog)).toBe('English')
  })

  it('names content from before languages were recorded (D64)', () => {
    expect(languageName('und', catalog)).toBe('Language not recorded')
  })
})

describe('orderLanguages', () => {
  it('puts the default first, then catalog order', () => {
    expect(orderLanguages(['tr', 'en'], 'en', catalog)).toEqual(['en', 'tr'])
    expect(orderLanguages(['en', 'tr'], null, catalog)).toEqual(['tr', 'en'])
  })

  it('keeps legacy content after the catalog languages', () => {
    expect(orderLanguages(['und', 'en'], 'en', catalog)).toEqual(['en', 'und'])
  })
})

// The audience step: who speaks what, per chosen channel, and what is suggested (D64).
describe('audienceByLanguage', () => {
  const counts = {
    total: 10,
    sms: 9,
    email: 6,
    by_language: { en: { total: 1, sms: 1, email: 0 }, tr: { total: 9, sms: 8, email: 6 } },
  }

  it('lists each language in catalog order with its share and the suggestion', () => {
    expect(audienceByLanguage(counts, ['sms', 'email'], ['tr', 'en'], catalog)).toEqual([
      { code: 'tr', name: 'Türkçe', total: 9, share: 90, sms: 8, email: 6, recommended: true },
      { code: 'en', name: 'English', total: 1, share: 10, sms: 1, email: 0, recommended: true },
    ])
  })

  it('counts only the chosen channels and leaves out languages nobody speaks', () => {
    const rows = audienceByLanguage({ ...counts, by_language: { tr: counts.by_language.tr } }, ['sms'], ['tr'], catalog)
    expect(rows).toEqual([{ code: 'tr', name: 'Türkçe', total: 9, share: 90, sms: 8, email: null, recommended: true }])
  })

  it('is empty without counts', () => {
    expect(audienceByLanguage(undefined, ['sms'], [], catalog)).toEqual([])
  })
})

// The default before one is saved: the saved one, the audience's suggestion,
// then the first catalog language. An empty audience suggests none (D64).
describe('startingDefault', () => {
  it('keeps the saved default', () => {
    expect(startingDefault('en', 'tr', catalog)).toBe('en')
  })

  it('takes the suggestion without a saved default', () => {
    expect(startingDefault(null, 'en', catalog)).toBe('en')
  })

  it('falls back to the catalog when the audience suggests nothing', () => {
    expect(startingDefault(null, '', catalog)).toBe('tr')
    expect(startingDefault(null, undefined, catalog)).toBe('tr')
  })
})

// What "Write with AI" writes: ticked languages without saved text (D66).
describe('toWrite', () => {
  it('lists the ticked languages without saved text', () => {
    expect(toWrite(['tr', 'en'], { tr: {} })).toEqual(['en'])
  })

  it('writes nothing until legacy text has its language set (D64)', () => {
    expect(toWrite(['und', 'en'], { und: {} })).toEqual([])
  })
})
