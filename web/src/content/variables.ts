// The only variables messages may use (D12).
export const VARIABLES = ['first_name', 'last_name', 'city'] as const
export type Variable = (typeof VARIABLES)[number]

// insertVariable puts {{v}} at the cursor (or over the selection).
export function insertVariable(text: string, start: number, end: number, v: Variable) {
  const token = `{{${v}}}`
  return { text: text.slice(0, start) + token + text.slice(end), cursor: start + token.length }
}
