// Fixes from the independent review of PR #6, each checked in the browser:
// double submits, signing out with unsaved text, the stale-content warning on
// the review step, a status stream that fails for good, pausing and
// cancelling, paging past the end, and summary links. The live part sends to
// every SMS customer through the mock provider and cancels after a moment.
import { ANKARA_BRIEF, BASE, CART_BRIEF, suite, putContent } from './lib.mjs'

export default () =>
  suite('review-fixes', async (t) => {
    const { page, check } = t
    await t.signIn()

    // a double click on Interpret creates one campaign: exactly one create
    // request from this page (other files may create campaigns meanwhile)
    const creates = []
    page.on('request', (r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/campaigns' && creates.push(r))
    await page.goto(`${BASE}/campaigns/new`)
    await page.locator('#brief').fill(CART_BRIEF)
    await page.getByRole('button', { name: 'Interpret with AI' }).dblclick()
    await page.waitForURL((u) => /^\/campaigns\/\d+$/.test(u.pathname))
    t.track(Number(new URL(page.url()).pathname.split('/')[2]))
    await page.waitForTimeout(1500) // a second request would have been sent by now
    check(creates.length === 1, `a double click on Interpret sends one create request (${creates.length})`)

    // signing out right after typing keeps the text
    const id = await t.draft(ANKARA_BRIEF, ['sms'])
    await putContent(page, id, 1, { sms: { body: 'Merhaba {{first_name}}!' } })
    await page.goto(`${BASE}/campaigns/${id}/content`)
    // A slow save, still on its way when the marketer signs out.
    await page.route(`**/api/campaigns/${id}/content`, async (r) => {
      if (r.request().method() === 'PUT') await new Promise((ok) => setTimeout(ok, 1500))
      await r.continue()
    })
    await page.locator('#sms-body').fill('Merhaba {{first_name}}, typed just before signing out')
    await page.getByRole('button', { name: /Demo Marketer/ }).click()
    await page.getByRole('menuitem', { name: 'Log out' }).click()
    await page.waitForURL(/\/login/)
    await page.unroute(`**/api/campaigns/${id}/content`)
    await t.signIn()
    check((await t.get(id)).content.tr.sms.body === 'Merhaba {{first_name}}, typed just before signing out', 'signing out saves the text typed just before')

    // the review step warns when the brief was re-interpreted after the messages
    await page.goto(`${BASE}/campaigns/${id}`)
    await page.getByRole('button', { name: 'Re-interpret brief' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Re-interpret' }).click()
    await page.getByText('All changes saved').waitFor()
    await page.goto(`${BASE}/campaigns/${id}/review`)
    await page.getByRole('heading', { name: 'When should it send?' }).waitFor()
    check(await page.getByText('The messages were written for the earlier brief', { exact: false }).isVisible(), 'the review step warns that the messages may not match the new brief')

    // a stream that fails for good says so and can be retried
    const live = await t.draft(ANKARA_BRIEF, ['sms'])
    const cities = (await (await page.request.get(`${BASE}/api/catalog`)).json()).cities
    await page.request.put(`${BASE}/api/campaigns/${live}/rules`, { data: { version: 1, rules: [{ field: 'city', op: 'in', value: cities }] } })
    await putContent(page, live, 2, { sms: { body: 'Merhaba {{first_name}}!' } })
    check((await page.request.post(`${BASE}/api/campaigns/${live}/approve`, { data: { version: 3, send: 'now' } })).ok(), 'test setup: sent now to every SMS customer')
    await page.route(`**/api/campaigns/${live}/events`, (r) => r.fulfill({ status: 500, body: 'down' }))
    await page.goto(`${BASE}/campaigns/${live}`)
    await page.getByText('Live updates stopped', { exact: false }).waitFor()
    check(true, 'a stream the browser gives up on is reported, not "loading" forever')
    await page.unroute(`**/api/campaigns/${live}/events`)
    await page.getByRole('button', { name: 'Try again' }).click()
    await page.getByText('Counts as of', { exact: false }).waitFor()
    check(true, 'trying again reconnects')

    // pausing hides "Live"; cancelling while paused updates the page
    await page.getByRole('button', { name: 'Live · pause' }).click()
    check((await page.getByText('Live', { exact: true }).count()) === 0, 'a paused page does not say "Live"')
    await page.getByRole('button', { name: 'Cancel campaign…' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel campaign' }).click()
    await page.getByText('Read-only results').waitFor({ timeout: 60000 })
    check((await page.getByRole('button', { name: /pause|resume/ }).count()) === 0, 'cancelling while paused shows the cancelled campaign')

    // summary links open a group with messages; paging past the end goes back to the last page
    await page.getByRole('link', { name: /^Not sent/ }).click()
    const drawer = page.getByRole('dialog', { name: 'Recipient details' })
    await drawer.waitFor()
    const group = new URL(page.url()).searchParams.get('recipients')
    check(group === 'cancelled' || group === 'skipped', `"Not sent" opens a group with messages (${group})`)
    await page.goto(`${BASE}/campaigns/${live}?recipients=cancelled&page=999`)
    await drawer.getByRole('button', { name: 'Previous page' }).click()
    const total = (await (await page.request.get(`${BASE}/api/campaigns/${live}/recipients?group=cancelled`)).json()).total
    await page.waitForURL((u) => Number(u.searchParams.get('page') ?? 1) === Math.max(1, Math.ceil(total / 50)))
    check(true, 'Previous from past the end goes to the last page')
  })
