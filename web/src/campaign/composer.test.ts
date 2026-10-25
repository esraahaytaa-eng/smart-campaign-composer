import { describe, expect, it } from 'vitest'
import { createSaver } from './saver'
import { ruleKey, withoutRule, withRule } from './rules'
import type { Rule } from './types'

const rule = (field: string, op: string, value: unknown): Rule => ({ field, op, value })
const city = rule('city', 'in', ['Ankara'])
const cart = rule('cart_added', 'within', { last_days: 30 })
const noOrder = rule('no_order', 'within', { last_days: 30 })

describe('withRule / withoutRule', () => {
  it('replaces the rule being edited, even when its operator changes', () => {
    const orders = rule('order_count', 'gte', 2)
    const edited = rule('order_count', 'lte', 5) // a different operator, so a different key
    expect(ruleKey(edited)).not.toBe(ruleKey(orders))
    expect(withRule([city, orders, noOrder], ruleKey(orders), edited)).toEqual([city, edited, noOrder])
  })

  it('replaces a rule whose value changes', () => {
    const edited = rule('cart_added', 'within', { last_days: 14 })
    expect(withRule([city, cart, noOrder], ruleKey(cart), edited)).toEqual([city, edited, noOrder])
  })

  it('appends a new rule', () => {
    expect(withRule([city], null, cart)).toEqual([city, cart])
  })

  it('appends an edited rule that was removed meanwhile, so the edit is not lost', () => {
    expect(withRule([city], ruleKey(cart), cart)).toEqual([city, cart])
  })

  // All customers stands alone (D14): choosing it replaces the filters (the
  // screen confirms first), and adding a filter replaces it.
  it('keeps All customers as the only rule', () => {
    const all = rule('all_customers', 'is', true)
    expect(withRule([city, cart], null, all)).toEqual([all])
    expect(withRule([all], null, city)).toEqual([city])
  })

  it('removes by rule, not by position', () => {
    expect(withoutRule([city, cart, noOrder], ruleKey(cart))).toEqual([city, noOrder])
    expect(withoutRule([city], ruleKey(cart))).toEqual([city])
  })
})

// The autosave bug from the T16 review: quick edits built their rule list
// when clicked, so a later save sent a stale list and undid an earlier one.
describe('queued rule saves', () => {
  type Draft = { version: number; rules: Rule[] }

  function fakeServer(start: Rule[]) {
    let stored: Draft = { version: 1, rules: start }
    let page: Draft = stored
    const saver = createSaver<Draft>(
      () => page.version,
      (d) => (page = d),
    )
    // Builds the payload when the save runs, from the page's latest state.
    const saveRules = (build: (rules: Rule[]) => Rule[]) =>
      saver.save(async (version) => {
        const rules = build(page.rules)
        await new Promise((r) => setTimeout(r, 5))
        if (version !== stored.version) throw new Error('stale version')
        stored = { version: version + 1, rules }
        return stored
      })
    return { saveRules, stored: () => stored, page: () => page }
  }

  it('keeps both of two quick removals', async () => {
    const s = fakeServer([city, cart, noOrder])

    await Promise.all([s.saveRules((r) => withoutRule(r, ruleKey(noOrder))), s.saveRules((r) => withoutRule(r, ruleKey(cart)))])

    expect(s.stored().rules).toEqual([city])
    expect(s.page()).toEqual(s.stored())
  })

  it('keeps an edit and a removal made in quick succession', async () => {
    const s = fakeServer([city, cart, noOrder])
    const edited = rule('cart_added', 'within', { last_days: 7 })

    await Promise.all([s.saveRules((r) => withRule(r, ruleKey(cart), edited)), s.saveRules((r) => withoutRule(r, ruleKey(noOrder)))])

    expect(s.stored().rules).toEqual([city, edited])
  })
})
