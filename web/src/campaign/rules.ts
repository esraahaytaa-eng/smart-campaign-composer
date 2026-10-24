// Rule chips (D9): plain-language labels in the UI language (D44), and the
// editor form that maps to and from the rule values the backend validates
// (D14, D39).
import { capitalize, formatAmount, formatDate, formatNumber, formatOr, t, type Key } from '../i18n'
import type { Catalog, FieldError, Rule, ValueType } from './types'

type Window = { last_days?: number; last_months?: number; from?: string; to?: string }
type RuleValue = Window & { categories?: string[]; amount?: number; count?: number }

export type RuleForm = {
  field: string
  op: string
  cities: string[]
  categories: string[]
  timeMode: 'last' | 'months' | 'since'
  lastDays: number
  lastMonths: number
  from: string
  to: string // optional end date; '' = until now
  amount: number // order count or TL amount
}

// Comparisons of a count or amount (D70: gt and lt beside gte and lte).
const COMPARE: Record<string, Record<string, Key>> = {
  order_count: { gte: 'rule.orderCountGte', lte: 'rule.orderCountLte', gt: 'rule.orderCountGt', lt: 'rule.orderCountLt' },
  total_spend: { gte: 'rule.totalSpendGte', lte: 'rule.totalSpendLte', gt: 'rule.totalSpendGt', lt: 'rule.totalSpendLt' },
  cart_value: { gte: 'rule.cartValueGte', gt: 'rule.cartValueGt' },
  spend_in_window: { gte: 'rule.spendWindowGte', lte: 'rule.spendWindowLte', gt: 'rule.spendWindowGt', lt: 'rule.spendWindowLt' },
  orders_in_window: { gte: 'rule.ordersWindowGte', lte: 'rule.ordersWindowLte', gt: 'rule.ordersWindowGt', lt: 'rule.ordersWindowLt' },
}

// "before" takes no end date: the backend rejects "to" there.
export const beforeOnly = (field: string, op: string) => op === 'before' && (field === 'last_order' || field === 'signed_up')

// "in the last 30 days (counted from send time)", "since 1 Oct 2026", …
function windowPhrase(w: Window): string {
  if (w.last_days !== undefined) return t('window.last', { count: w.last_days })
  if (w.last_months !== undefined) return t('window.lastMonths', { count: w.last_months })
  return w.to ? t('window.range', { from: formatDate(w.from ?? ''), to: formatDate(w.to) }) : t('window.since', { date: formatDate(w.from ?? '') })
}

// ruleLabel is a chip's text in the UI language (D9, D44). Cities and
// category codes are data: they are shown exactly as stored.
export function ruleLabel(r: Rule): string {
  const v = r.value as RuleValue
  const window = () => windowPhrase(v)
  const label = (() => {
    const compare = COMPARE[r.field]?.[r.op]
    if (compare) {
      const total = typeof r.value === 'number'
      const n = total ? (r.value as number) : (v.amount ?? v.count ?? 0)
      return t(compare, { amount: formatAmount(n), orders: t('rules.orders', { count: n }), window: total ? '' : window() })
    }
    switch (r.field) {
      case 'city':
        return t('rule.city', { list: formatOr(r.value as string[]) })
      case 'cart_added':
        return t('rule.cartAdded', { window: window() })
      case 'no_order':
        return t('rule.noOrder', { window: window() })
      case 'signed_up':
        if (r.op === 'before') {
          if (v.last_days !== undefined) return t('rule.signedUpBeforeDays', { count: v.last_days })
          if (v.last_months !== undefined) return t('rule.signedUpBeforeMonths', { count: v.last_months })
          return t('rule.signedUpBeforeDate', { date: formatDate(v.from ?? '') })
        }
        return t('rule.signedUp', { window: window() })
      case 'last_order':
        if (r.op === 'after') return t('rule.lastOrderAfter', { window: window() })
        if (v.last_days !== undefined) return t('rule.lastOrderBeforeDays', { count: v.last_days })
        if (v.last_months !== undefined) return t('rule.lastOrderBeforeMonths', { count: v.last_months })
        return t('rule.lastOrderBeforeDate', { date: formatDate(v.from ?? '') })
      case 'ordered_category':
        return t('rule.orderedCategory', { list: formatOr(v.categories ?? []), window: window() })
      case 'carted_category':
        return t('rule.cartedCategory', { list: formatOr(v.categories ?? []), window: window() })
    }
    return `${r.field} ${r.op}` // a field this screen doesn't know yet
  })()
  return capitalize(label) // Turkish labels can start with the time window
}

export function valueType(field: string, catalog: Catalog): ValueType | undefined {
  return catalog.fields.find((f) => f.field === field)?.value
}

export function defaultForm(field: string, op: string, catalog: Catalog): RuleForm {
  const type = valueType(field, catalog)
  const amount = type === 'count' || type === 'count_window' ? 1 : 0
  return { field, op, cities: [], categories: [], timeMode: 'last', lastDays: 30, lastMonths: 12, from: '', to: '', amount }
}

export function toForm(r: Rule, catalog: Catalog): RuleForm {
  const form = defaultForm(r.field, r.op, catalog)
  const v = r.value as RuleValue
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
    case 'amount_window':
    case 'count_window':
      form.amount = v.amount ?? v.count ?? 0
      Object.assign(form, windowForm(v))
      break
    case 'count':
    case 'amount':
      form.amount = r.value as number
  }
  return form
}

function windowForm(w: Window): Partial<RuleForm> {
  if (w.last_days !== undefined) return { timeMode: 'last', lastDays: w.last_days }
  if (w.last_months !== undefined) return { timeMode: 'months', lastMonths: w.last_months }
  return { timeMode: 'since', from: w.from ?? '', to: w.to ?? '' }
}

export function fromForm(form: RuleForm, catalog: Catalog): Rule {
  const window = (): Window => {
    if (form.timeMode === 'last') return { last_days: form.lastDays }
    if (form.timeMode === 'months') return { last_months: form.lastMonths }
    // "before" is "before a date": it takes no end date.
    const to = beforeOnly(form.field, form.op) ? '' : form.to
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
      case 'amount_window':
        return { amount: form.amount, ...window() }
      case 'count_window':
        return { count: form.amount, ...window() }
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
  'city.in': 'title.city',
  'cart_added.within': 'title.cartAdded',
  'no_order.within': 'title.noOrder',
  'signed_up.within': 'title.signedUp',
  'signed_up.before': 'title.signedUpBefore',
  'last_order.before': 'title.lastOrderBefore',
  'last_order.after': 'title.lastOrderAfter',
  'ordered_category.in': 'title.orderedCategory',
  'carted_category.in': 'title.cartedCategory',
  'order_count.gte': 'title.orderCountGte',
  'order_count.lte': 'title.orderCountLte',
  'order_count.gt': 'title.orderCountGt',
  'order_count.lt': 'title.orderCountLt',
  'total_spend.gte': 'title.totalSpendGte',
  'total_spend.lte': 'title.totalSpendLte',
  'total_spend.gt': 'title.totalSpendGt',
  'total_spend.lt': 'title.totalSpendLt',
  'cart_value.gte': 'title.cartValueGte',
  'cart_value.gt': 'title.cartValueGt',
  'spend_in_window.gte': 'title.spendWindowGte',
  'spend_in_window.lte': 'title.spendWindowLte',
  'spend_in_window.gt': 'title.spendWindowGt',
  'spend_in_window.lt': 'title.spendWindowLt',
  'orders_in_window.gte': 'title.ordersWindowGte',
  'orders_in_window.lte': 'title.ordersWindowLte',
  'orders_in_window.gt': 'title.ordersWindowGt',
  'orders_in_window.lt': 'title.ordersWindowLt',
}

// ruleTitle names a field and operator in the "+ Add rule" menu and the editor.
export function ruleTitle(field: string, op: string): string {
  const key = TITLES[`${field}.${op}`]
  return key ? t(key) : `${field} ${op}`
}

// formProblems catches obvious mistakes before a rule is saved; the backend
// validates everything again (D14).
export function formProblems(form: RuleForm, catalog: Catalog): string[] {
  const { max_days, max_months, max_count, max_amount, min_date } = catalog.limits
  const type = valueType(form.field, catalog)
  const problems: string[] = []
  if (type === 'cities' && form.cities.length === 0) problems.push(t('problem.city'))
  if (type === 'categories_window' && form.categories.length === 0) problems.push(t('problem.category'))
  if (type === 'window' || type === 'categories_window' || type === 'amount_window' || type === 'count_window') {
    if (form.timeMode === 'last') {
      if (!Number.isInteger(form.lastDays) || form.lastDays < 1 || form.lastDays > max_days) problems.push(t('problem.days', { max: max_days }))
    } else if (form.timeMode === 'months') {
      if (!Number.isInteger(form.lastMonths) || form.lastMonths < 1 || form.lastMonths > max_months) problems.push(t('problem.months', { max: max_months }))
    } else if (!form.from) {
      problems.push(t('problem.startDate'))
    } else if (form.from < min_date) {
      problems.push(t('problem.minDate', { date: formatDate(min_date) }))
    } else if (form.to && form.to < form.from && !beforeOnly(form.field, form.op)) {
      problems.push(t('problem.endBeforeStart'))
    }
  }
  if ((type === 'count' || type === 'count_window') && !(Number.isInteger(form.amount) && form.amount >= 0 && form.amount <= max_count)) {
    problems.push(t('problem.count', { max: formatNumber(max_count) }))
  }
  if ((type === 'amount' || type === 'amount_window') && !(Number.isFinite(form.amount) && form.amount >= 0 && form.amount <= max_amount)) {
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
