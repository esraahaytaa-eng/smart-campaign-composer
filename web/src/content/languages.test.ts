import { describe, expect, it } from 'vitest'
import { languageName, orderLanguages } from './languages'

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
