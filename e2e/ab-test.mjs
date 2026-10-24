// An optional A/B test (D68): version B is written on request only, both
// versions are approved per language, each recipient gets one version, and
// the report shows real delivery data only, never a winner. Sends to the
// mock SMS provider only, to the ~60 SMS customers in Adana.
import { ANKARA_BRIEF, BASE, suite, putContent } from './lib.mjs'

const sms = { sms: { body: 'Merhaba {{first_name}}, hafta sonu indirimi başladı!' } }

export default () =>
  suite('ab-test', async (t) => {
    const { page, check } = t
    const saved = () => page.getByText('All changes saved').waitFor()
    const tab = page.locator('.tabs').first().getByRole('button', { name: /^Türkçe/ })
    await t.signIn()

    // without a test, nothing about versions is shown
    const plain = await t.draft(ANKARA_BRIEF, ['sms'])
    await page.goto(`${BASE}/campaigns/${plain}/content`)
    await page.locator('#sms-body').waitFor()
    check((await page.getByRole('button', { name: 'Version B', exact: true }).count()) === 0, 'a campaign without a test has no versions')

    const id = await t.draft(ANKARA_BRIEF, ['sms'])
    await page.request.put(`${BASE}/api/campaigns/${id}/rules`, { data: { version: 1, rules: [{ field: 'city', op: 'in', value: ['Adana'] }] } })
    await putContent(page, id, 2, sms)
    const writes = []
    page.on('request', (r) => r.method() === 'POST' && r.url().endsWith(`/campaigns/${id}/content`) && writes.push(r.postDataJSON()))

    // turning the test on: approval waits for version B
    await page.goto(`${BASE}/campaigns/${id}/content`)
    await page.locator('#sms-body').waitFor()
    await page.getByLabel('Compare two versions (A/B test)').check()
    await tab.getByText('Version B missing').waitFor()
    check((await t.get(id)).ab_test === true, 'the A/B test is on')
    const blocked = await page.request.post(`${BASE}/api/campaigns/${id}/approve`, { data: { version: (await t.get(id)).version, send: 'now' } })
    check(blocked.status() === 422, 'the campaign cannot be approved without version B')

    // version B with AI: one request, for version B only
    await page.getByRole('button', { name: 'Version B', exact: true }).click()
    await page.getByRole('button', { name: 'Write version B with AI' }).click()
    await page.locator('.ai-bar').getByText('The AI wrote the messages').waitFor({ timeout: 30000 })
    let c = await t.get(id)
    check(writes.length === 1 && writes[0].variant === 'b' && writes[0].languages.join() === 'tr', 'one AI request, for version B in Turkish')
    check(c.content.tr.sms.body === sms.sms.body && !!c.content_b?.tr?.sms?.body && c.content_b.tr.sms.body !== c.content.tr.sms.body, 'version A is unchanged and B differs')
    check(c.languages.tr.approved_at === null, 'Turkish needs review with its new version B')
    check((await page.locator('#sms-body').inputValue()) === c.content_b.tr.sms.body, 'the B editor shows version B')

    // Copy A, then an edit: saved, needs review again, no AI call
    await page.getByRole('button', { name: 'Copy version A' }).click()
    check((await page.locator('#sms-body').inputValue()) === sms.sms.body, 'Copy version A fills version B with A')
    await page.locator('#sms-body').fill(`${sms.sms.body} Son gün!`)
    await page.locator('#sms-body').blur()
    await saved()
    c = await t.get(id)
    check(c.content_b.tr.sms.body === `${sms.sms.body} Son gün!` && writes.length === 1, 'the hand-written version B is saved without an AI call')
    await page.getByRole('button', { name: 'Approve Türkçe' }).click()
    await tab.getByText('Approved').waitFor()

    // review: both versions side by side
    await page.goto(`${BASE}/campaigns/${id}/review`)
    await page.getByRole('heading', { name: 'Version B' }).waitFor()
    check(await page.getByRole('heading', { name: 'Version A' }).isVisible(), 'the review shows version A next to B')
    check(await page.getByText('About half of each language gets each version.').isVisible(), 'the review says how the split works')

    // send and report: per version, delivery only
    c = await t.get(id)
    const sent = await page.request.post(`${BASE}/api/campaigns/${id}/approve`, { data: { version: c.version, send: 'now' } })
    check(sent.ok(), 'the A/B campaign is approved and sent')
    check(await t.completed(id), 'the campaign completes')
    await page.goto(`${BASE}/campaigns/${id}`)
    await page.getByText('Read-only results').waitFor({ timeout: 60000 })
    const report = page.locator('.versions-report')
    check(await report.getByText("Delivery only: this app doesn't record opens, clicks or orders").isVisible(), 'the report says it has delivery data only')
    const rows = await report.locator('tbody tr').allInnerTexts()
    const recipients = rows.map((r) => Number(r.split('\t')[1].replace(/\D/g, '')))
    const groups = (await (await page.request.get(`${BASE}/api/campaigns/${id}/recipients?group=sent`)).json()).groups.sms
    const total = Object.values(groups).reduce((a, b) => a + b, 0)
    check(rows.length === 2 && recipients[0] + recipients[1] === total && Math.abs(recipients[0] - recipients[1]) <= 1, `both versions, split ${recipients.join('/')} of ${total}`)
    check((await page.getByText(/winner|better version/i).count()) === 0, 'no winner is named')
    const b = await (await page.request.get(`${BASE}/api/campaigns/${id}/recipients?group=sent&variant=b`)).json()
    check(b.recipients.every((r) => r.variant === 'b'), 'the recipients list filters by version')
    await t.shot('ab-report')
  })
