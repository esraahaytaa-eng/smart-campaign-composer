import { describe, expect, it } from 'vitest'
import { render, SAMPLES, samplesFor } from './preview'

// The preview fills templates like the backend's content.Render, with
// fictional customers only (T17, option A: no customer data on screen).
describe('render', () => {
  const ayse = { first_name: 'Ayşe', last_name: 'Yılmaz', city: 'İstanbul' }

  it('fills every variable', () => {
    expect(render('Merhaba {{first_name}} {{last_name}}, {{city}} mağazası', ayse)).toBe('Merhaba Ayşe Yılmaz, İstanbul mağazası')
  })

  it('tidies the space an empty value leaves, like the backend', () => {
    expect(render('Hi {{first_name}} {{last_name}} there', { ...ayse, last_name: '' })).toBe('Hi Ayşe there')
    expect(render('  {{last_name}} hi  ', { ...ayse, last_name: '' })).toBe('hi')
  })

  it('keeps line breaks', () => {
    expect(render('Merhaba {{first_name}},\n\nİndirim', ayse)).toBe('Merhaba Ayşe,\n\nİndirim')
  })

  it('leaves anything that is not a variable as written', () => {
    expect(render('{{email}} {first_name}', ayse)).toBe('{{email}} {first_name}')
  })

  it('offers only clearly fictional samples, one with a long name', () => {
    expect(SAMPLES.map((s) => s.id)).toEqual(['typical', 'long'])
    const long = SAMPLES.find((s) => s.id === 'long')!.vars
    expect([...`${long.first_name} ${long.last_name}`].length).toBeGreaterThan(20)
  })

  // D64: each language's tab previews with customers who speak it.
  it('has samples per language, Turkish for anything else', () => {
    const en = samplesFor('en')
    expect(en.map((s) => s.id)).toEqual(['typical', 'long'])
    expect(en[0]!.vars.first_name).not.toBe(SAMPLES[0]!.vars.first_name)
    expect([...`${en[1]!.vars.first_name} ${en[1]!.vars.last_name}`].length).toBeGreaterThan(20)
    expect(samplesFor('tr')).toBe(SAMPLES)
    expect(samplesFor('und')).toBe(SAMPLES)
  })
})
