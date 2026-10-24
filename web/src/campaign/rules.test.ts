import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '../i18n'
import type { Catalog, Rule } from './types'
import { addableRules, defaultForm, formProblems, fromForm, ruleErrors, ruleLabel, ruleTitle, toForm } from './rules'

const catalog: Catalog = {
  fields: [
    { field: 'cart_added', ops: ['within'], value: 'window' },
    { field: 'cart_value', ops: ['gte', 'gt'], value: 'amount' },
    { field: 'carted_category', ops: ['in'], value: 'categories_window' },
    { field: 'city', ops: ['in'], value: 'cities' },
    { field: 'last_order', ops: ['before', 'after'], value: 'window' },
    { field: 'no_order', ops: ['within'], value: 'window' },
    { field: 'order_count', ops: ['gte', 'lte', 'gt', 'lt'], value: 'count' },
    { field: 'ordered_category', ops: ['in'], value: 'categories_window' },
    { field: 'orders_in_window', ops: ['gte', 'lte', 'gt', 'lt'], value: 'count_window' },
    { field: 'signed_up', ops: ['within', 'before'], value: 'window' },
    { field: 'spend_in_window', ops: ['gte', 'lte', 'gt', 'lt'], value: 'amount_window' },
    { field: 'total_spend', ops: ['gte', 'lte', 'gt', 'lt'], value: 'amount' },
  ],
  cities: ['Ankara', 'İstanbul', 'İzmir'],
  categories: ['books', 'fashion'],
  limits: { max_rules: 20, max_days: 365, max_months: 12, max_count: 10000, max_amount: 1000000, min_date: '2000-01-01' },
  languages: [{ code: 'tr', name: 'Türkçe' }, { code: 'en', name: 'English' }],
}

const rule = (field: string, op: string, value: unknown): Rule => ({ field, op, value })

describe('ruleLabel', () => {
  it.each([
    [rule('city', 'in', ['İstanbul']), 'City is İstanbul'],
    [rule('city', 'in', ['Ankara', 'İstanbul', 'İzmir']), 'City is Ankara, İstanbul or İzmir'],
    [rule('cart_added', 'within', { last_days: 30 }), 'Added to cart in the last 30 days (counted from send time)'],
    [rule('no_order', 'within', { from: '2026-10-01' }), 'No order since 1 Oct 2026'],
    [rule('signed_up', 'within', { from: '2026-10-01', to: '2026-10-10' }), 'Signed up from 1 Oct 2026 to 10 Oct 2026'],
    [rule('signed_up', 'within', { last_days: 1 }), 'Signed up in the last day (counted from send time)'],
    [rule('last_order', 'before', { last_days: 90 }), 'Last order more than 90 days ago (counted from send time)'],
    [rule('last_order', 'before', { from: '2026-07-01' }), 'Last order before 1 Jul 2026'],
    [rule('last_order', 'after', { last_days: 14 }), 'Last order in the last 14 days (counted from send time)'],
    [rule('ordered_category', 'in', { categories: ['books'], last_days: 30 }), 'Ordered books in the last 30 days (counted from send time)'],
    [rule('carted_category', 'in', { categories: ['books', 'fashion'], from: '2026-10-01' }), 'Added books or fashion to cart since 1 Oct 2026'],
    [rule('order_count', 'gte', 1), 'At least 1 order'],
    [rule('order_count', 'lte', 3), 'At most 3 orders'],
    [rule('total_spend', 'gte', 10000), 'Spent at least 10,000 TL in total'],
    [rule('total_spend', 'lte', 499.5), 'Spent at most 499.50 TL in total'],
    [rule('cart_value', 'gte', 5000), 'At least 5,000 TL in the cart'],
    // D70
    [rule('cart_value', 'gt', 5000), 'More than 5,000 TL in the cart'],
    [rule('order_count', 'gt', 3), 'More than 3 orders'],
    [rule('order_count', 'lt', 1), 'Fewer than 1 order'],
    [rule('total_spend', 'gt', 10000), 'Spent more than 10,000 TL in total'],
    [rule('total_spend', 'lt', 500), 'Spent less than 500 TL in total'],
    [rule('cart_added', 'within', { last_months: 1 }), 'Added to cart in the last month (counted from send time)'],
    [rule('spend_in_window', 'gt', { amount: 10000, last_months: 12 }), 'Spent more than 10,000 TL in the last 12 months (counted from send time)'],
    [rule('spend_in_window', 'gte', { amount: 500, from: '2026-10-01' }), 'Spent at least 500 TL since 1 Oct 2026'],
    [rule('spend_in_window', 'lte', { amount: 100, last_days: 30 }), 'Spent at most 100 TL in the last 30 days (counted from send time)'],
    [rule('spend_in_window', 'lt', { amount: 100, last_days: 30 }), 'Spent less than 100 TL in the last 30 days (counted from send time)'],
    [rule('orders_in_window', 'gte', { count: 4, last_months: 12 }), 'At least 4 orders in the last 12 months (counted from send time)'],
    [rule('orders_in_window', 'lte', { count: 1, last_days: 30 }), 'At most 1 order in the last 30 days (counted from send time)'],
    [rule('orders_in_window', 'gt', { count: 2, last_days: 30 }), 'More than 2 orders in the last 30 days (counted from send time)'],
    [rule('orders_in_window', 'lt', { count: 2, last_days: 30 }), 'Fewer than 2 orders in the last 30 days (counted from send time)'],
    [rule('signed_up', 'before', { last_days: 7 }), 'Signed up more than 7 days ago (counted from send time)'],
    [rule('signed_up', 'before', { last_months: 1 }), 'Signed up more than 1 month ago (counted from send time)'],
    [rule('signed_up', 'before', { from: '2026-10-01' }), 'Signed up before 1 Oct 2026'],
    [rule('last_order', 'before', { last_months: 6 }), 'Last order more than 6 months ago (counted from send time)'],
  ])('%j reads "%s"', (r, label) => {
    expect(ruleLabel(r)).toBe(label)
  })
})

// The plan's required test: chips map to rules and back, in both time forms.
describe('chip ↔ rule mapping', () => {
  it.each([
    rule('city', 'in', ['İstanbul', 'Ankara']),
    rule('cart_added', 'within', { last_days: 30 }),
    rule('no_order', 'within', { from: '2026-10-01' }),
    rule('signed_up', 'within', { from: '2026-10-01', to: '2026-10-10' }),
    rule('last_order', 'before', { last_days: 90 }),
    rule('last_order', 'after', { from: '2026-09-01' }),
    rule('ordered_category', 'in', { categories: ['books'], last_days: 30 }),
    rule('carted_category', 'in', { categories: ['fashion'], from: '2026-10-01', to: '2026-10-20' }),
    rule('order_count', 'lte', 3),
    rule('total_spend', 'gte', 10000),
    rule('cart_value', 'gte', 5000),
    rule('cart_value', 'gt', 5000),
    rule('cart_added', 'within', { last_months: 3 }),
    rule('signed_up', 'before', { last_days: 7 }),
    rule('signed_up', 'before', { from: '2026-10-01' }),
    rule('spend_in_window', 'gt', { amount: 10000, last_months: 12 }),
    rule('spend_in_window', 'lte', { amount: 99.5, from: '2026-01-01', to: '2026-06-30' }),
    rule('orders_in_window', 'gte', { count: 4, last_days: 90 }),
  ])('round-trips %j', (r) => {
    expect(fromForm(toForm(r, catalog), catalog)).toEqual(r)
  })

  it('switches a time rule from the last N days to a fixed date and back', () => {
    const form = toForm(rule('cart_added', 'within', { last_days: 30 }), catalog)
    form.timeMode = 'since'
    form.from = '2026-10-01'
    expect(fromForm(form, catalog)).toEqual(rule('cart_added', 'within', { from: '2026-10-01' }))

    form.timeMode = 'last'
    expect(fromForm(form, catalog)).toEqual(rule('cart_added', 'within', { last_days: 30 }))
  })

  it('switches a time rule to calendar months', () => {
    const form = toForm(rule('spend_in_window', 'gte', { amount: 100, last_days: 30 }), catalog)
    form.timeMode = 'months'
    form.lastMonths = 12
    expect(fromForm(form, catalog)).toEqual(rule('spend_in_window', 'gte', { amount: 100, last_months: 12 }))
  })

  it('never sends an end date for "signed up before"', () => {
    const form = toForm(rule('signed_up', 'within', { from: '2026-09-01', to: '2026-09-30' }), catalog)
    form.op = 'before'
    expect(fromForm(form, catalog)).toEqual(rule('signed_up', 'before', { from: '2026-09-01' }))
  })

  it('starts a new order count in a period at 1 order in the last 30 days', () => {
    expect(fromForm(defaultForm('orders_in_window', 'gte', catalog), catalog)).toEqual(rule('orders_in_window', 'gte', { count: 1, last_days: 30 }))
  })

  it('drops an end date the marketer cleared', () => {
    const form = toForm(rule('signed_up', 'within', { from: '2026-10-01', to: '2026-10-10' }), catalog)
    form.to = ''
    expect(fromForm(form, catalog)).toEqual(rule('signed_up', 'within', { from: '2026-10-01' }))
  })

  // The backend rejects "to" on last_order before (it is "before a date").
  it('never sends an end date for "last order before"', () => {
    const form = toForm(rule('last_order', 'after', { from: '2026-09-01', to: '2026-09-30' }), catalog)
    form.op = 'before'
    expect(fromForm(form, catalog)).toEqual(rule('last_order', 'before', { from: '2026-09-01' }))
  })

  it('starts a new time rule at the last 30 days', () => {
    expect(fromForm(defaultForm('no_order', 'within', catalog), catalog)).toEqual(rule('no_order', 'within', { last_days: 30 }))
  })
})

describe('addableRules', () => {
  it('offers every field and operator not used yet', () => {
    const used = [rule('city', 'in', ['Ankara']), rule('order_count', 'gte', 1)]
    const offered = addableRules(used, catalog).map((r) => `${r.field} ${r.op}`)

    expect(offered).not.toContain('city in')
    expect(offered).not.toContain('order_count gte')
    expect(offered).toContain('order_count lte')
    expect(offered).toHaveLength(27 - 2)
  })

  it('offers nothing at the rule limit', () => {
    const many = Array.from({ length: 20 }, () => rule('city', 'in', ['Ankara']))
    expect(addableRules(many, catalog)).toEqual([])
  })
})

describe('ruleErrors', () => {
  it('groups server field errors by rule, and keeps list-wide ones apart', () => {
    const errors = ruleErrors([
      { rule: 1, message: 'unknown city "Paris"' },
      { rule: -1, message: 'add at least one audience rule' },
      { rule: 1, message: 'second problem' },
    ])
    expect(errors.byRule.get(1)).toEqual(['unknown city "Paris"', 'second problem'])
    expect(errors.general).toEqual(['add at least one audience rule'])
  })
})

describe('ruleTitle', () => {
  it('names every field and operator in the catalog', () => {
    for (const f of catalog.fields) for (const op of f.ops) expect(ruleTitle(f.field, op)).not.toBe(`${f.field} ${op}`)
  })
})

// Obvious mistakes are caught before Apply; the backend still validates (D14).
describe('formProblems', () => {
  const form = (field: string, op: string, patch: object = {}) => Object.assign(defaultForm(field, op, catalog), patch)

  it('accepts a complete rule', () => {
    expect(formProblems(form('city', 'in', { cities: ['Ankara'] }), catalog)).toEqual([])
    expect(formProblems(form('cart_added', 'within'), catalog)).toEqual([])
  })

  it.each([
    [form('city', 'in'), 'Choose at least one city.'],
    [form('ordered_category', 'in'), 'Choose at least one category.'],
    [form('cart_added', 'within', { lastDays: 0 }), 'Enter 1 to 365 days.'],
    [form('cart_added', 'within', { lastDays: 2.5 }), 'Enter 1 to 365 days.'],
    [form('cart_added', 'within', { timeMode: 'since' }), 'Choose a start date.'],
    [form('cart_added', 'within', { timeMode: 'since', from: '1999-12-31' }), 'Choose a date from 1 Jan 2000.'],
    [form('signed_up', 'within', { timeMode: 'since', from: '2026-10-10', to: '2026-10-01' }), 'The end date must not be before the start date.'],
    [form('order_count', 'gte', { amount: -1 }), 'Enter a whole number from 0 to 10,000.'],
    [form('order_count', 'gte', { amount: 1.5 }), 'Enter a whole number from 0 to 10,000.'],
    [form('total_spend', 'gte', { amount: 2000000 }), 'Enter an amount from 0 to 1,000,000 TL.'],
    [form('total_spend', 'gte', { amount: Number.NaN }), 'Enter an amount from 0 to 1,000,000 TL.'],
    // A cleared v-model.number input holds '', which `'' >= 0` lets through.
    [form('total_spend', 'gte', { amount: '' }), 'Enter an amount from 0 to 1,000,000 TL.'],
    [form('spend_in_window', 'gte', { amount: '' }), 'Enter an amount from 0 to 1,000,000 TL.'],
    [form('order_count', 'gte', { amount: '' }), 'Enter a whole number from 0 to 10,000.'],
    [form('cart_added', 'within', { lastDays: '' }), 'Enter 1 to 365 days.'],
    [form('cart_added', 'within', { timeMode: 'months', lastMonths: 0 }), 'Enter 1 to 12 months.'],
    [form('cart_added', 'within', { timeMode: 'months', lastMonths: 13 }), 'Enter 1 to 12 months.'],
    [form('spend_in_window', 'gte', { amount: -1 }), 'Enter an amount from 0 to 1,000,000 TL.'],
    [form('orders_in_window', 'gte', { amount: 1.5 }), 'Enter a whole number from 0 to 10,000.'],
    [form('orders_in_window', 'gte', { lastDays: 400 }), 'Enter 1 to 365 days.'],
  ])('%j: %s', (f, problem) => {
    expect(formProblems(f, catalog)).toEqual([problem])
  })
})

// D44: chips are UI text in the marketer's language; the data inside them
// (cities, categories, numbers, dates) is never translated.
describe('Turkish chip labels', () => {
  afterEach(() => setLocale('en'))

  it.each([
    [rule('city', 'in', ['İstanbul', 'Ankara']), 'Şehir: İstanbul veya Ankara'],
    [rule('cart_added', 'within', { last_days: 30 }), 'Son 30 gün içinde (gönderim zamanına göre) sepete ürün ekledi'],
    [rule('no_order', 'within', { from: '2026-10-01' }), '1 Eki 2026 tarihinden beri sipariş vermedi'],
    [rule('signed_up', 'within', { from: '2026-10-01', to: '2026-10-10' }), '1 Eki 2026 ile 10 Eki 2026 arasında üye oldu'],
    [rule('last_order', 'before', { last_days: 90 }), 'Son siparişi 90 günden daha eski (gönderim zamanına göre)'],
    [rule('ordered_category', 'in', { categories: ['books'], last_days: 7 }), 'Son 7 gün içinde (gönderim zamanına göre) books kategorisinden sipariş verdi'],
    [rule('order_count', 'gte', 3), 'En az 3 sipariş'],
    [rule('total_spend', 'gte', 10000), 'Toplam harcaması en az 10.000 TL'],
    [rule('cart_value', 'gte', 499.5), 'Sepet tutarı en az 499,50 TL'],
    [rule('cart_value', 'gt', 5000), 'Sepet tutarı 5.000 TL’den fazla'],
    [rule('order_count', 'gt', 3), '3 siparişten fazla'],
    [rule('order_count', 'lt', 1), '1 siparişten az'],
    [rule('total_spend', 'gt', 10000), 'Toplam harcaması 10.000 TL’den fazla'],
    [rule('total_spend', 'lt', 500), 'Toplam harcaması 500 TL’den az'],
    [rule('spend_in_window', 'gt', { amount: 10000, last_months: 12 }), 'Son 12 ay içinde (gönderim zamanına göre) 10.000 TL’den fazla harcadı'],
    [rule('spend_in_window', 'gte', { amount: 500, last_days: 30 }), 'Son 30 gün içinde (gönderim zamanına göre) en az 500 TL harcadı'],
    [rule('orders_in_window', 'gte', { count: 4, last_months: 12 }), 'Son 12 ay içinde (gönderim zamanına göre) en az 4 sipariş verdi'],
    [rule('orders_in_window', 'lt', { count: 2, last_days: 30 }), 'Son 30 gün içinde (gönderim zamanına göre) 2 siparişten az verdi'],
    [rule('signed_up', 'before', { last_days: 7 }), 'Üyeliği 7 günden daha eski (gönderim zamanına göre)'],
    [rule('signed_up', 'before', { from: '2026-10-01' }), '1 Eki 2026 tarihinden önce üye oldu'],
    [rule('last_order', 'before', { last_months: 6 }), 'Son siparişi 6 aydan daha eski (gönderim zamanına göre)'],
  ])('%j reads "%s"', async (r, label) => {
    await setLocale('tr')
    expect(ruleLabel(r)).toBe(label)
  })

  it('translates editor titles and problems', async () => {
    await setLocale('tr')
    expect(ruleTitle('city', 'in')).toBe('Şehir')
    expect(formProblems(defaultForm('city', 'in', catalog), catalog)).toEqual(['En az bir şehir seçin.'])
    expect(formProblems(Object.assign(defaultForm('total_spend', 'gte', catalog), { amount: -1 }), catalog)).toEqual(['0 ile 1.000.000 TL arasında bir tutar girin.'])
  })
})
