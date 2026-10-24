import { describe, expect, it } from 'vitest'
import { audienceByLanguage, autoWrite, chosenDefault, chosenLanguages, languageName, orderLanguages, startingDefault, toWrite, writesFirst } from './languages'

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

// One-step creation (D71): the audience step's choice, carried in the URL.
describe('chosenLanguages', () => {
  it('keeps catalog codes once, in catalog order', () => {
    expect(chosenLanguages('en,tr,en', catalog)).toEqual(['tr', 'en'])
  })

  it('drops unknown codes, legacy text and anything not a single string', () => {
    expect(chosenLanguages('xx,und,,en', catalog)).toEqual(['en'])
    expect(chosenLanguages(['tr'], catalog)).toEqual([])
    expect(chosenLanguages(undefined, catalog)).toEqual([])
  })
})

describe('writesFirst', () => {
  const draft = { status: 'draft', content: null, parent_campaign_id: null } as const
  it('is a new draft without text', () => {
    expect(writesFirst(draft)).toBe(true)
  })

  it('never for saved text, version B text, a resend or a non-draft', () => {
    expect(writesFirst({ ...draft, content: { tr: { sms: { body: 'x' } } } })).toBe(false)
    expect(writesFirst({ ...draft, content_b: { tr: { sms: { body: 'x' } } } })).toBe(false)
    expect(writesFirst({ ...draft, parent_campaign_id: 7 })).toBe(false)
    expect(writesFirst({ ...draft, status: 'approved' })).toBe(false)
  })
})

describe('autoWrite', () => {
  const draft = { status: 'draft', content: null, parent_campaign_id: null } as const
  it('writes the chosen languages when asked', () => {
    expect(autoWrite({ write: 'ai', languages: 'en,tr' }, draft, catalog)).toEqual(['tr', 'en'])
  })

  it('never without the signal, without languages, or with text already there', () => {
    expect(autoWrite({ languages: 'tr' }, draft, catalog)).toEqual([])
    expect(autoWrite({ write: 'yes', languages: 'tr' }, draft, catalog)).toEqual([])
    expect(autoWrite({ write: 'ai', languages: 'xx' }, draft, catalog)).toEqual([])
    expect(autoWrite({ write: 'ai', languages: 'tr' }, { ...draft, content: { tr: { sms: { body: 'x' } } } }, catalog)).toEqual([])
    expect(autoWrite({ write: 'ai', languages: 'tr' }, { ...draft, parent_campaign_id: 7 }, catalog)).toEqual([])
  })
})

describe('chosenDefault', () => {
  it('keeps the suggested default if chosen, else the first chosen', () => {
    expect(chosenDefault(['tr', 'en'], 'tr')).toBe('tr')
    expect(chosenDefault(['en'], 'tr')).toBe('en')
    expect(chosenDefault([], 'tr')).toBe('tr')
  })
})
