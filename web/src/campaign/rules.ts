// Rule chips (D9): plain-language labels in the UI language (D44), and the
// editor form that maps to and from the rule values the backend validates
// (D14, D39).
import { capitalize, formatAmount, formatDate, formatNumber, formatOr, t, type Key } from '../i18n'
import type { Catalog, FieldError, Rule, ValueType } from './types'

type Window = { last_days?: number; from?: string; to?: string }

export type RuleForm = {
  field: string
  op: string
  cities: string[]
  categories: string[]
  timeMode: 'last' | 'since'
  lastDays: number
  from: string
  to: string // optional end date; '' = until now
  amount: number // order count or TL amount
}

// "in the last 30 days (counted from send time)", "since 1 Oct 2026", …
function windowPhrase(w: Window): string {
  if (w.last_days !== undefined) return t('window.last', { count: w.last_days })
  return w.to ? t('window.range', { from: formatDate(w.from ?? ''), to: formatDate(w.to) }) : t('window.since', { date: formatDate(w.from ?? '') })
}

// ruleLabel is a chip's text in the UI language (D9, D44). Cities and
// category codes are data: they are shown exactly as stored.
export function ruleLabel(r: Rule): string {
  const v = r.value as Window & { categories?: string[] }
  const window = () => windowPhrase(v)
  const label = (() => {
    switch (r.field) {
      case 'city':
        return t('rule.city', { list: formatOr(r.value as string[]) })
      case 'cart_added':
        return t('rule.cartAdded', { window: window() })
      case 'no_order':
        return t('rule.noOrder', { window: window() })
      case 'signed_up':
        return t('rule.signedUp', { window: window() })
      case 'last_order':
        if (r.op === 'after') return t('rule.lastOrderAfter', { window: window() })
        return v.last_days !== undefined
          ? t('rule.lastOrderBeforeDays', { count: v.last_days })
          : t('rule.lastOrderBeforeDate', { date: formatDate(v.from ?? '') })
      case 'ordered_category':
        return t('rule.orderedCategory', { list: formatOr(v.categories ?? []), window: window() })
      case 'carted_category':
        return t('rule.cartedCategory', { list: formatOr(v.categories ?? []), window: window() })
      case 'order_count': {
        const orders = t('rules.orders', { count: r.value as number })
        return t(r.op === 'gte' ? 'rule.orderCountGte' : 'rule.orderCountLte', { orders })
      }
      case 'total_spend':
        return t(r.op === 'gte' ? 'rule.totalSpendGte' : 'rule.totalSpendLte', { amount: formatAmount(r.value as number) })
      case 'cart_value':
        return t('rule.cartValueGte', { amount: formatAmount(r.value as number) })
    }
    return `${r.field} ${r.op}` // a field this screen doesn't know yet
  })()
  return capitalize(label) // Turkish labels can start with the time window
}

export function valueType(field: string, catalog: Catalog): ValueType | undefined {
  return catalog.fields.find((f) => f.field === field)?.value
}

export function defaultForm(field: string, op: string, catalog: Catalog): RuleForm {
  return { field, op, cities: [], categories: [], timeMode: 'last', lastDays: 30, from: '', to: '', amount: valueType(field, catalog) === 'count' ? 1 : 0 }
}

export function toForm(r: Rule, catalog: Catalog): RuleForm {
  const form = defaultForm(r.field, r.op, catalog)
  const v = r.value as Window & { categories?: string[] }
  switch (valueType(r.field, catalog)) {
    case 'cities':
      form.cities = [...(r.value as string[])]
      break
    case 'categories_window':
      form.categories = [...(v.categories ?? [])]
      Object.assign(form, windowForm(v))
      break
    case 'window':
      Object.assign(form, windowForm(v))
      break
    case 'count':
    case 'amount':
      form.amount = r.value as number
  }
  return form
}

function windowForm(w: Window): Partial<RuleForm> {
  return w.last_days !== undefined ? { timeMode: 'last', lastDays: w.last_days } : { timeMode: 'since', from: w.from ?? '', to: w.to ?? '' }
}

export function fromForm(form: RuleForm, catalog: Catalog): Rule {
  const window = (): Window => {
    if (form.timeMode === 'last') return { last_days: form.lastDays }
    // "last order before" is "before a date": it takes no end date.
    const to = form.field === 'last_order' && form.op === 'before' ? '' : form.to
    return to ? { from: form.from, to } : { from: form.from }
  }
  const value = (() => {
    switch (valueType(form.field, catalog)) {
      case 'cities':
        return [...form.cities]
      case 'categories_window':
        return { categories: [...form.categories], ...window() }
      case 'window':
        return window()
      default:
        return form.amount
    }
  })()
  return { field: form.field, op: form.op, value }
}

// addableRules lists the field/operator pairs "+ Add rule" offers: the
// backend allows each pair once, and at most max_rules rules.
export function addableRules(rules: Rule[], catalog: Catalog): { field: string; op: string }[] {
  if (rules.length >= catalog.limits.max_rules) return []
  const used = new Set(rules.map((r) => `${r.field} ${r.op}`))
  return catalog.fields.flatMap((f) => f.ops.map((op) => ({ field: f.field, op }))).filter((p) => !used.has(`${p.field} ${p.op}`))
}

// ruleErrors groups the backend's field errors by chip; rule -1 is about the
// list as a whole (for example "add at least one audience rule").
export function ruleErrors(errors: FieldError[]): { byRule: Map<number, string[]>; general: string[] } {
  const byRule = new Map<number, string[]>()
  const general: string[] = []
  for (const e of errors) {
    if (e.rule < 0) general.push(e.message)
    else byRule.set(e.rule, [...(byRule.get(e.rule) ?? []), e.message])
  }
  return { byRule, general }
}

const TITLES: Record<string, Key> = {
  'city in': 'title.city',
  'cart_added within': 'title.cartAdded',
  'no_order within': 'title.noOrder',
  'signed_up within': 'title.signedUp',
  'last_order before': 'title.lastOrderBefore',
  'last_order after': 'title.lastOrderAfter',
  'ordered_category in': 'title.orderedCategory',
  'carted_category in': 'title.cartedCategory',
  'order_count gte': 'title.orderCountGte',
  'order_count lte': 'title.orderCountLte',
  'total_spend gte': 'title.totalSpendGte',
  'total_spend lte': 'title.totalSpendLte',
  'cart_value gte': 'title.cartValueGte',
}

// ruleTitle names a field and operator in the "+ Add rule" menu and the editor.
export function ruleTitle(field: string, op: string): string {
  const key = TITLES[`${field} ${op}`]
  return key ? t(key) : `${field} ${op}`
}

// formProblems catches obvious mistakes before a rule is saved; the backend
// validates everything again (D14).
export function formProblems(form: RuleForm, catalog: Catalog): string[] {
  const { max_days, max_count, max_amount, min_date } = catalog.limits
  const type = valueType(form.field, catalog)
  const problems: string[] = []
  if (type === 'cities' && form.cities.length === 0) problems.push(t('problem.city'))
  if (type === 'categories_window' && form.categories.length === 0) problems.push(t('problem.category'))
  if (type === 'window' || type === 'categories_window') {
    if (form.timeMode === 'last') {
      if (!Number.isInteger(form.lastDays) || form.lastDays < 1 || form.lastDays > max_days) problems.push(t('problem.days', { max: max_days }))
    } else if (!form.from) {
      problems.push(t('problem.startDate'))
    } else if (form.from < min_date) {
      problems.push(t('problem.minDate', { date: formatDate(min_date) }))
    } else if (form.to && form.to < form.from && !(form.field === 'last_order' && form.op === 'before')) {
      problems.push(t('problem.endBeforeStart'))
    }
  }
  if (type === 'count' && !(Number.isInteger(form.amount) && form.amount >= 0 && form.amount <= max_count)) {
    problems.push(t('problem.count', { max: formatNumber(max_count) }))
  }
  if (type === 'amount' && !(form.amount >= 0 && form.amount <= max_amount)) {
    problems.push(t('problem.amount', { max: formatNumber(max_amount) }))
  }
  return problems
}

// A rule's identity: the backend allows each field and operator once (D14).
export const ruleKey = (r: Rule) => `${r.field} ${r.op}`

// withRule replaces the rule with key (the one being edited) or appends a new
// one. It is applied when a save runs, to the latest rules, so queued edits
// never undo each other.
export function withRule(rules: Rule[], key: string | null, next: Rule): Rule[] {
  const i = key === null ? -1 : rules.findIndex((r) => ruleKey(r) === key)
  return i < 0 ? [...rules, next] : rules.map((r, j) => (j === i ? next : r))
}

export function withoutRule(rules: Rule[], key: string): Rule[] {
  return rules.filter((r) => ruleKey(r) !== key)
}
