// Shared helpers for the browser suite. Each test file signs in, creates the
// campaigns it needs, checks behaviour against the API, and cleans up: drafts
// are deleted, approved campaigns cancelled (so nothing is ever sent).
import { chromium } from 'playwright'

export const BASE = process.env.BASE ?? 'http://localhost:8080'
const SHOTS = process.env.SHOTS // set to a directory to keep screenshots
const EMAIL = process.env.DEMO_EMAIL ?? 'marketer@marketer.com'
const PASSWORD = process.env.DEMO_PASSWORD ?? 'Demo1234!'

export async function suite(name, body) {
  const browser = await chromium.launch()
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, locale: 'en-GB' })
  const page = await context.newPage()
  const problems = []
  const created = []
  page.on('pageerror', (e) => problems.push(`page error: ${e.message}`))
  const t = {
    page,
    context,
    check(ok, what) {
      console.log(`${ok ? 'PASS' : 'FAIL'} ${name}: ${what}`)
      if (!ok) problems.push(what)
    },
    async shot(file) {
      if (SHOTS) await page.screenshot({ path: `${SHOTS}/${file}.png`, fullPage: true })
    },
    track(id) {
      created.push(id)
      return id
    },
    async get(id) {
      return (await page.request.get(`${BASE}/api/campaigns/${id}`)).json()
    },
    async signIn() {
      await page.goto(BASE + '/login')
      await page.getByLabel('Work email').fill(EMAIL)
      await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
      await page.getByRole('button', { name: 'Sign in' }).click()
      await page.waitForURL((u) => !u.pathname.startsWith('/login'))
    },
    // A draft from a brief the mock AI understands, through the API.
    async draft(request, channels = ['sms', 'email']) {
      const r = await page.request.post(`${BASE}/api/campaigns`, { data: { request, channels, time_zone: 'Europe/Istanbul' } })
      return t.track((await r.json()).id)
    },
  }
  try {
    await body(t)
  } catch (err) {
    problems.push(`stopped: ${err.message.split('\n')[0]}`)
    console.log(`FAIL ${name}: stopped: ${err.message.split('\n')[0]}`)
  } finally {
    for (const id of created) {
      const c = await t.get(id).catch(() => null)
      if (c?.status === 'draft') await page.request.delete(`${BASE}/api/campaigns/${id}`, { data: { version: c.version } })
      else if (c?.status === 'approved' || c?.status === 'sending') await page.request.post(`${BASE}/api/campaigns/${id}/cancel`)
    }
    await browser.close()
  }
  console.log(problems.length ? `${name}: ${problems.length} problem(s)` : `${name}: all checks passed`)
  return problems.length === 0
}

export const CART_BRIEF = 'Send a 30% discount reminder to users in Istanbul who added products to their cart in the last 30 days but did not buy anything.'
export const ANKARA_BRIEF = 'Send a weekend sale announcement to all users in Ankara. Send it tomorrow at 10:00.'
export const WELCOME_BRIEF = 'Send a welcome discount to users who signed up in the last 7 days.'
