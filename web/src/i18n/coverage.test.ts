import { describe, expect, it } from 'vitest'

// D44: every screen gets its words from the dictionaries. This scan keeps it
// that way for future screens: text a person can read or hear must come from
// t(). A line marked `i18n-ignore` is skipped (developer-only messages).
const components = import.meta.glob('../**/*.vue', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
const modules = import.meta.glob(['../**/*.ts', '!../**/*.test.ts', '!./en.ts', '!./tr.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

const letters = /[A-Za-zÀ-ÖØ-öø-ÿĞğİıŞş]/ // letters only: not × or ÷
const keyLike = /^[a-z][\w-]*(\.[\w${}-]+)+$/ // 'auth.signIn', `status.${s}`
const literal = /'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"|`((?:[^`\\]|\\.)*)`/g
// Attributes a person reads or hears, static or bound (component props too).
// (aria-hidden, -labelledby, -current… hold ids or states, not text.)
const userAttr =
  /\s(:|v-bind:)?(aria-(?:label|description|roledescription|valuetext|placeholder)|title|placeholder|alt|label|[\w-]+-label|v-text|v-html)=("([^"]*)"|'([^']*)')/g

const visible = (s: string) => letters.test(s.replace(/\$\{[^}]*\}/g, '').replace(/&\w+;/g, ''))
// In code, a sentence or phrase: words with a space, or a word ending like one.
const phrase = (s: string) => {
  const text = s.replace(/\$\{[^}]*\}/g, ' ')
  return visible(text) && !keyLike.test(s) && (/[A-Za-zÀ-ÿĞğİıŞş]\s+[A-Za-zÀ-ÿĞğİıŞş]/.test(text) || /[A-Za-zÀ-ÿ][.!?…]$/.test(text))
}
// In a template expression, a phrase or a capitalised word ('Save'); lowercase
// single words are code values ('done', 'sms').
const uiLiteral = (s: string) => phrase(s) || (/^[A-ZÀ-ÖØ-ÞĞİŞ]/.test(s) && visible(s) && !keyLike.test(s))
const literals = (code: string) => [...code.matchAll(literal)].map((m) => m[1] ?? m[2] ?? m[3] ?? '')
const codeLines = (code: string) =>
  code
    .split('\n')
    .filter((l) => !l.includes('i18n-ignore') && !/^\s*(\/\/|\*|\/\*)/.test(l))
    .map((l) => l.replace(/\s\/\/.*$/, ''))

export function hardcodedInComponent(source: string): string[] {
  const found: string[] = []
  const script = source.match(/<script[\s\S]*?<\/script>/)?.[0] ?? ''
  const template = source.replace(/<script[\s\S]*?<\/script>/g, '').replace(/<style[\s\S]*?<\/style>/g, '').replace(/<!--[\s\S]*?-->/g, '')
  for (const m of template.matchAll(userAttr)) {
    const [, bound, attr, , dq, sq] = m
    const value = dq ?? sq ?? ''
    if (bound || attr === 'v-text' || attr === 'v-html') {
      for (const lit of literals(value)) if (uiLiteral(lit)) found.push(`${attr}: '${lit}'`)
    } else if (visible(value)) found.push(`${attr}="${value}"`)
  }
  for (const [, expr] of template.matchAll(/\{\{([\s\S]*?)\}\}/g)) {
    for (const lit of literals(expr!)) if (uiLiteral(lit)) found.push(`{{ '${lit}' }}`)
  }
  const text = template.replace(/\{\{[\s\S]*?\}\}/g, ' ').replace(/<(?:[^>"']|"[^"]*"|'[^']*')*>/g, '\n')
  for (const line of text.split('\n')) if (visible(line)) found.push(`text: ${line.trim()}`)
  found.push(...hardcodedInCode(script))
  return found
}

export function hardcodedInCode(code: string): string[] {
  return codeLines(code).flatMap((l) => literals(l).filter(phrase).map((lit) => `script: ${lit}`))
}

describe('the scanner', () => {
  const vue = (template: string, script = '') => `<script setup lang="ts">${script}</script>\n<template>${template}</template>`

  it.each([
    ['template text', vue('<p>Hello world</p>')],
    ['a literal in a mustache', vue(`<p>{{ busy ? 'Saving…' : 'Save' }}</p>`)],
    ['a bound attribute literal', vue(`<button :aria-label="'Close dialog'">×</button>`)],
    ['a component label prop', vue(`<ConfirmDialog :confirm-label="'Delete it'" />`)],
    ['v-text', vue(`<p v-text="'Hello there'" />`)],
    ['a single-quoted static attribute', vue(`<input placeholder='Your name' />`)],
    ['aria-description', vue('<div aria-description="Opens the menu" />')],
    ['a short script string', vue('<p />', "const s = 'Saving…'")],
    ['an apostrophe', vue('<p />', `const m = "Can't reach the server."`)],
    ['a lowercase phrase', vue('<p />', "const m = 'not saved'")],
  ])('catches %s', (_, source) => {
    expect(hardcodedInComponent(source)).not.toEqual([])
  })

  it.each([
    ['translated text', vue(`<p>{{ t('auth.signIn') }}</p><a :aria-label="t('composer.back')">×</a>`)],
    ['ids and states', vue(`<p aria-hidden="true" aria-labelledby="t1" :aria-current="on ? 'step' : undefined">{{ s === 'done' ? '✓' : n }}</p>`)],
    ['a key built from data', vue('<span>{{ t(`status.${s}`) }}</span>')],
    ['entities and symbols', vue('<p>&nbsp;× → —</p>')],
    ['class names', vue(`<p :class="busy ? 'btn btn-primary' : 'btn'" />`)],
    ['codes in script', vue('<p />', "const a = '/api/me'; const b = 'en-GB'; const c = 'sms'")],
    ['an ignored developer message', vue('<p />', "throw new Error('Something broke here.') // i18n-ignore")],
  ])('allows %s', (_, source) => {
    expect(hardcodedInComponent(source)).toEqual([])
  })
})

describe('UI text coverage', () => {
  it('scans the components and modules', () => {
    expect(Object.keys(components).length).toBeGreaterThan(10)
    expect(Object.keys(modules).length).toBeGreaterThan(8)
  })

  it.each(Object.keys(components))('%s has no hard-coded UI text', (file) => {
    expect(hardcodedInComponent(components[file]!)).toEqual([])
  })

  it.each(Object.keys(modules))('%s has no hard-coded UI text', (file) => {
    expect(hardcodedInCode(modules[file]!)).toEqual([])
  })
})
