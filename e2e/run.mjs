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

// The dashboard check compares counts over all campaigns, so it runs alone,
// first. Then two lanes run side by side: UI checks that send nothing, and
// the checks that send, one after another (sends share the provider's
// 100/s limit, so running them together would make their timings wobble).
const alone = ['campaigns.mjs']
const lanes = [
  ['sign-in.mjs', 'composer.mjs', 'saving.mjs', 'languages.mjs', 'content-review.mjs'],
  ['delivery.mjs', 'recipients.mjs', 'resend.mjs', 'live.mjs', 'review-fixes.mjs'],
]
const times = []
async function run(f) {
  const start = Date.now()
  console.log(`\n=== ${f}`)
  const ok = await (await import(`./${f}`)).default()
  times.push([f, Math.round((Date.now() - start) / 1000)])
  return ok
}
const started = Date.now()
let ok = true
for (const f of alone) ok = (await run(f)) && ok
const results = await Promise.all(
  lanes.map(async (lane) => {
    let laneOk = true
    for (const f of lane) laneOk = (await run(f)) && laneOk
    return laneOk
  }),
)
ok = results.every(Boolean) && ok
console.log('\nTimes: ' + times.map(([f, s]) => `${f} ${s}s`).join(', ') + `; total ${Math.round((Date.now() - started) / 1000)}s`)
console.log(ok ? '\nALL BROWSER CHECKS PASSED' : '\nSOME BROWSER CHECKS FAILED')
process.exit(ok ? 0 : 1)
