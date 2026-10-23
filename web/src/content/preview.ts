// Message preview with fictional customers (T17, option A): no real customer
// data is shown. Fills templates exactly like the backend's content.Render.
export type Vars = { first_name: string; last_name: string; city: string }

// Fictional people; "long" shows how a long name affects the length.
export const SAMPLES: { id: 'typical' | 'long'; vars: Vars }[] = [
  { id: 'typical', vars: { first_name: 'Ayşe', last_name: 'Yılmaz', city: 'İstanbul' } },
  { id: 'long', vars: { first_name: 'Abdurrahman Kerim', last_name: 'Yıldızoğlu', city: 'Afyonkarahisar' } }, // i18n-ignore: sample data
]

export function render(template: string, vars: Vars): string {
  let s = template
  for (const v of ['first_name', 'last_name', 'city'] as const) s = s.split(`{{${v}}}`).join(vars[v]) // backend order
  return s.replace(/ {2,}/g, ' ').replace(/^ +| +$/g, '')
}
