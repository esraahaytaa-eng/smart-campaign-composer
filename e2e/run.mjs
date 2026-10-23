// Runs every browser test file in order; exits non-zero if any check fails.
import { BASE } from './lib.mjs'

// The checks rely on the mock AI's answers, and the real AI costs money:
// refuse to run unless the app is in mock mode.
const login = await fetch(`${BASE}/api/login`, {
  method: 'POST',
  body: JSON.stringify({ email: process.env.DEMO_EMAIL ?? 'marketer@marketer.com', password: process.env.DEMO_PASSWORD ?? 'Demo1234!' }),
})
const cookie = (login.headers.get('set-cookie') ?? '').split(';')[0]
const me = login.ok ? await (await fetch(`${BASE}/api/me`, { headers: { cookie } })).json() : null
if (me?.llm_mode !== 'mock') {
  console.log(me ? `make e2e needs LLM_MODE=mock (the app runs with ${me.llm_mode}).` : `Can't sign in at ${BASE}: start the app with docker compose up.`)
  process.exit(1)
}
await fetch(`${BASE}/api/logout`, { method: 'POST', headers: { cookie } })

const files = ['sign-in.mjs', 'composer.mjs', 'saving.mjs', 'languages.mjs', 'content-review.mjs', 'campaigns.mjs', 'delivery.mjs']
let ok = true
for (const f of files) {
  console.log(`\n=== ${f}`)
  const mod = await import(`./${f}`)
  ok = (await mod.default()) && ok
}
console.log(ok ? '\nALL BROWSER CHECKS PASSED' : '\nSOME BROWSER CHECKS FAILED')
process.exit(ok ? 0 : 1)
