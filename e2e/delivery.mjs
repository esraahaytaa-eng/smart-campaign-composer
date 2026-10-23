// The campaign page after approval (T18 step 5, boards 4 and B): a scheduled
// campaign and its cancel, and live delivery until completed. The live case
// really sends, to the mock SMS provider only, to the ~60 SMS customers in
// Adana, so the run finishes in seconds.
import { ANKARA_BRIEF, BASE, suite } from './lib.mjs'

const sms = { sms: { body: 'Merhaba {{first_name}}, hafta sonu indirimi başladı!' } }

export default () =>
  suite('delivery', async (t) => {
    const { page, check } = t
    await t.signIn()
    const approve = async (id, send) => {
      await page.request.put(`${BASE}/api/campaigns/${id}/content`, { data: { version: 1, content: sms } })
      return page.request.post(`${BASE}/api/campaigns/${id}/approve`, { data: { version: 2, send } })
    }

    // scheduled: send time, estimate, locked content, cancel
    const scheduled = await t.draft(ANKARA_BRIEF, ['sms'])
    const at = new Date(Date.now() + 24 * 3600 * 1000)
    check((await approve(scheduled, { at: at.toISOString() })).ok(), 'test setup: scheduled for tomorrow')
    await page.goto(`${BASE}/campaigns/${scheduled}`)
    await page.getByText('Sending starts', { exact: true }).waitFor()
    check(await page.getByText('Estimated at approval', { exact: false }).first().isVisible(), 'the approval estimate is shown')
    check(await page.getByText('Approved content · locked').isVisible(), 'the approved content is shown, locked')
    check(await page.getByText(sms.sms.body).isVisible(), 'the SMS text is shown as written')
    check((await page.getByRole('button', { name: /Write with AI|Delete draft/ }).count()) === 0, 'nothing can be edited after approval')
    await t.shot('scheduled')
    await page.getByRole('button', { name: 'Cancel campaign…' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel campaign' }).click()
    await page.getByText('Cancelled before sending started. No messages were created.').waitFor()
    check((await t.get(scheduled)).status === 'cancelled', 'the scheduled campaign is cancelled')

    // live delivery until completed
    const live = await t.draft(ANKARA_BRIEF, ['sms'])
    await page.request.put(`${BASE}/api/campaigns/${live}/rules`, { data: { version: 1, rules: [{ field: 'city', op: 'in', value: ['Adana'] }] } })
    await page.request.put(`${BASE}/api/campaigns/${live}/content`, { data: { version: 2, content: sms } })
    const sent = await page.request.post(`${BASE}/api/campaigns/${live}/approve`, { data: { version: 3, send: 'now' } })
    check(sent.ok(), 'test setup: sent now to Adana')
    const streams = []
    page.on('request', (r) => r.url().endsWith(`/api/campaigns/${live}/events`) && streams.push(Date.now()))
    await page.goto(`${BASE}/campaigns/${live}`)
    await page.getByRole('heading', { name: 'SMS', exact: false }).first().waitFor()
    check(await page.getByText('Counts as of', { exact: false }).isVisible(), 'the page says when the counts were taken')
    await page.getByText('Read-only results').waitFor({ timeout: 120000 })
    const after = await t.get(live)
    check(after.status === 'completed', 'the page shows completed once the campaign is')
    const groups = (await (await page.request.get(`${BASE}/api/campaigns/${live}/recipients?group=sent`)).json()).groups.sms
    const total = Object.values(groups).reduce((a, b) => a + b, 0)
    const totalCard = page.locator('.summary .total .value')
    check((await totalCard.innerText()).replace(/\D/g, '') === String(total), `total messages match the API (${total})`)
    const sentRow = page.locator('.channel-row', { hasText: 'Sent' }).first()
    check((await sentRow.innerText()).includes(String(groups.sent)), `the sent row matches the API (${groups.sent})`)
    check(await page.getByText('Actual when sending started', { exact: false }).isVisible(), 'estimate and actual audience are compared')
    check((await page.getByRole('button', { name: 'Cancel campaign…' }).count()) === 0, 'a finished campaign cannot be cancelled')
    await t.shot('completed')
    const opened = streams.length
    await page.waitForTimeout(5000)
    check(streams.length === opened, 'the stream is not reopened after the end (D36)')

    // pause and Turkish
    await page.goto(`${BASE}/campaigns/${live}`)
    await page.getByText('Read-only results').waitFor()
    await page.getByRole('button', { name: 'Türkçe' }).click()
    await page.getByText('Salt okunur sonuçlar').waitFor() // Turkish loads on first use (D58)
    check(await page.getByText('Salt okunur sonuçlar').isVisible(), 'the results are in Turkish')
    await page.getByRole('button', { name: 'English' }).click()
  })
