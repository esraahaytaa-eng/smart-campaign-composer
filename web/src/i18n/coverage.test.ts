import { describe, expect, it } from 'vitest'

// D44: every screen gets its words from the dictionaries. This scan keeps
// it that way for future screens too: text a person reads in a template, and
// sentence-like strings in a component's script, must come from t().
const components = import.meta.glob('../**/*.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

const letters = /[A-Za-zÀ-ÖØ-öø-ÿĞğİıŞş]/ // letters only: not × or ÷
const userAttrs = /\s(aria-label|title|placeholder|alt|label)="([^"]*)"/g // static, not :bound
const sentence = /(['"`])([A-Z][a-z]+(?:[ ,;:–-]+[A-Za-z’']+){2,}[.!?…]?)\1/g

function hardcoded(source: string): string[] {
  const found: string[] = []
  const template = source.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<style[\s\S]*?<\/style>/g, '').replace(/<!--[\s\S]*?-->/g, '')
  for (const [, attr, value] of template.matchAll(userAttrs)) if (letters.test(value!)) found.push(`${attr}="${value}"`)
  // Tags may contain "=>" inside quoted attribute values, so skip quoted parts.
  const text = template.replace(/\{\{[\s\S]*?\}\}/g, ' ').replace(/<(?:[^>"']|"[^"]*"|'[^']*')*>/g, '\n')
  for (const line of text.split('\n')) if (letters.test(line)) found.push(`text: ${line.trim()}`)
  const script = source.match(/<script[\s\S]*?<\/script>/)?.[0] ?? ''
  for (const [, , words] of script.matchAll(sentence)) found.push(`script: ${words}`)
  return found
}

describe('UI text coverage', () => {
  it('scans the components', () => {
    expect(Object.keys(components).length).toBeGreaterThan(10)
  })

  it.each(Object.keys(components))('%s has no hard-coded UI text', (file) => {
    expect(hardcoded(components[file]!)).toEqual([])
  })
})
