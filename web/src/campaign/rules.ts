// Rule chips (D9): plain-language labels, and the editor form that maps to
// and from the rule values the backend validates (D14, D39).
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

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

// "2026-10-01" → "1 Oct 2026", independent of the browser's locale.
export function formatDate(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number)
  return `${d} ${MONTHS[(m ?? 1) - 1]} ${y}`
}

function formatAmount(n: number): string {
  return n.toLocaleString('en-US', { minimumFractionDigits: Number.isInteger(n) ? 0 : 2, maximumFractionDigits: 2 })
}

function list(items: string[]): string {
  return items.length < 2 ? (items[0] ?? '') : `${items.slice(0, -1).join(', ')} or ${items[items.length - 1]}`
}

// "in the last 30 days (counted from send time)", "since 1 Oct 2026", …
function windowPhrase(w: Window): string {
  if (w.last_days !== undefined) {
    return `in the last ${w.last_days === 1 ? 'day' : `${w.last_days} days`} (counted from send time)`
  }
  return w.to ? `from ${formatDate(w.from ?? '')} to ${formatDate(w.to)}` : `since ${formatDate(w.from ?? '')}`
}

export function ruleLabel(r: Rule): string {
  const v = r.value as Window & { categories?: string[] } & string[] & number
  switch (r.field) {
    case 'city':
      return `City is ${list(r.value as string[])}`
    case 'cart_added':
      return `Added to cart ${windowPhrase(v)}`
    case 'no_order':
      return `No order ${windowPhrase(v)}`
    case 'signed_up':
      return `Signed up ${windowPhrase(v)}`
    case 'last_order':
      if (r.op === 'after') return `Last order ${windowPhrase(v)}`
      return v.last_days !== undefined
        ? `Last order more than ${v.last_days} days ago (counted from send time)`
        : `Last order before ${formatDate(v.from ?? '')}`
    case 'ordered_category':
      return `Ordered ${list(v.categories ?? [])} ${windowPhrase(v)}`
    case 'carted_category':
      return `Added ${list(v.categories ?? [])} to cart ${windowPhrase(v)}`
    case 'order_count': {
      const n = r.value as number
      return `${r.op === 'gte' ? 'At least' : 'At most'} ${n} ${n === 1 ? 'order' : 'orders'}`
    }
    case 'total_spend':
      return `Spent ${r.op === 'gte' ? 'at least' : 'at most'} ${formatAmount(r.value as number)} TL in total`
    case 'cart_value':
      return `At least ${formatAmount(r.value as number)} TL in the cart`
  }
  return `${r.field} ${r.op}` // a field this screen doesn't know yet
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

const TITLES: Record<string, string> = {
  'city in': 'City',
  'cart_added within': 'Added to cart',
  'no_order within': 'No order',
  'signed_up within': 'Signed up',
  'last_order before': 'Last order longer ago than',
  'last_order after': 'Last order within',
  'ordered_category in': 'Ordered a category',
  'carted_category in': 'Added a category to cart',
  'order_count gte': 'Number of orders: at least',
  'order_count lte': 'Number of orders: at most',
  'total_spend gte': 'Total spend: at least',
  'total_spend lte': 'Total spend: at most',
  'cart_value gte': 'Cart value: at least',
}

// ruleTitle names a field and operator in the "+ Add rule" menu and the editor.
export function ruleTitle(field: string, op: string): string {
  return TITLES[`${field} ${op}`] ?? `${field} ${op}`
}

// formProblems catches obvious mistakes before a rule is saved; the backend
// validates everything again (D14).
export function formProblems(form: RuleForm, catalog: Catalog): string[] {
  const { max_days, max_count, max_amount, min_date } = catalog.limits
  const type = valueType(form.field, catalog)
  const problems: string[] = []
  if (type === 'cities' && form.cities.length === 0) problems.push('Choose at least one city.')
  if (type === 'categories_window' && form.categories.length === 0) problems.push('Choose at least one category.')
  if (type === 'window' || type === 'categories_window') {
    if (form.timeMode === 'last') {
      if (!Number.isInteger(form.lastDays) || form.lastDays < 1 || form.lastDays > max_days) problems.push(`Enter 1 to ${max_days} days.`)
    } else if (!form.from) {
      problems.push('Choose a start date.')
    } else if (form.from < min_date) {
      problems.push(`Choose a date from ${formatDate(min_date)}.`)
    } else if (form.to && form.to < form.from && !(form.field === 'last_order' && form.op === 'before')) {
      problems.push('The end date must not be before the start date.')
    }
  }
  if (type === 'count' && !(Number.isInteger(form.amount) && form.amount >= 0 && form.amount <= max_count)) {
    problems.push(`Enter a whole number from 0 to ${max_count.toLocaleString('en-US')}.`)
  }
  if (type === 'amount' && !(form.amount >= 0 && form.amount <= max_amount)) {
    problems.push(`Enter an amount from 0 to ${max_amount.toLocaleString('en-US')} TL.`)
  }
  return problems
}
