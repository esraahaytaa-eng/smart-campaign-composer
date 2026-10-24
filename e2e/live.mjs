// Final QA for T18 (requested 2026-10-23): watch a campaign mid-send and
// cancel it while it sends. The campaign goes to every SMS customer (about
// 2,100) through the mock SMS provider, which at 100/s takes ~20 s, long
// enough to watch counts move, pause, resume and cancel before it ends.
import { ANKARA_BRIEF, BASE, suite } from './lib.mjs'

export default () =>
  suite('live', async (t) => {
    const { page, check } = t
    await t.signIn()
    const sentOnPage = async () => Number((await page.locator('.summary .stat').nth(1).locator('.value').innerText()).replace(/\D/g, ''))
    const asOf = () => page.getByText(/^Counts as of /).innerText()

    const id = await t.draft(ANKARA_BRIEF, ['sms'])
    const cities = (await (await page.request.get(`${BASE}/api/catalog`)).json()).cities
    const rules = await page.request.put(`${BASE}/api/campaigns/${id}/rules`, { data: { version: 1, rules: [{ field: 'city', op: 'in', value: cities }] } })
    check(rules.ok(), `test setup: every city (${cities.length}) as the audience`)
    await page.request.put(`${BASE}/api/campaigns/${id}/content`, { data: { version: 2, content: { sms: { body: 'Merhaba {{first_name}}, hafta sonu indirimi!' } } } })
    check((await page.request.post(`${BASE}/api/campaigns/${id}/approve`, { data: { version: 3, send: 'now' } })).ok(), 'test setup: sent now to every SMS customer')

    // mid-send: live, counts moving
    await page.goto(`${BASE}/campaigns/${id}`)
    await page.getByText('Live', { exact: true }).waitFor({ timeout: 30000 })
    const first = await sentOnPage()
    const firstTime = await asOf()
    await page.waitForTimeout(2500)
    const second = await sentOnPage()
    check(second > first, `the sent count moves while sending (${first} → ${second})`)
    check((await asOf()) !== firstTime, 'the "counts as of" time moves with it')
    check(await page.locator('.summary .stat').nth(3).locator('.value').innerText().then((v) => Number(v.replace(/\D/g, '')) > 0), 'messages are shown in progress')
    await t.shot('mid-send')

    // pause freezes the page, resume catches up
    await page.getByRole('button', { name: 'Live · pause' }).click()
    const paused = await sentOnPage()
    await page.waitForTimeout(2500)
    check((await sentOnPage()) === paused, 'pause stops the updates')
    check((await page.getByRole('button', { name: 'Paused · resume' }).getAttribute('aria-pressed')) === 'true', 'the button says it is paused')
    await page.getByRole('button', { name: 'Paused · resume' }).click()
    await page.waitForFunction((n) => {
      const v = document.querySelectorAll('.summary .stat')[1]?.querySelector('.value')?.textContent ?? '0'
      return Number(v.replace(/\D/g, '')) > n
    }, paused, { timeout: 10000 })
    check(true, 'resume catches up with the sends made meanwhile')

    // cancel while sending
    const before = await t.get(id)
    check(before.status === 'sending', 'still sending before the cancel')
    await page.getByRole('button', { name: 'Cancel campaign…' }).click()
    const dlg = page.getByRole('alertdialog')
    check(await dlg.getByText('Messages still pending or retrying become Cancelled.', { exact: false }).isVisible(), 'the dialog says what a cancel does while sending')
    check(await dlg.getByText('can’t be recalled', { exact: false }).isVisible(), 'the dialog says sent messages stay sent')
    await dlg.getByRole('button', { name: 'Cancel campaign' }).click()
    await page.getByText('Read-only results').waitFor({ timeout: 60000 })
    const after = await t.get(id)
    check(after.status === 'cancelled', 'the campaign is cancelled')
    const g = (await (await page.request.get(`${BASE}/api/campaigns/${id}/recipients?group=cancelled`)).json()).groups.sms
    check(g.cancelled > 0 && g.sent > 0, `sent messages stay sent (${g.sent}) and the rest are cancelled (${g.cancelled})`)
    check(g.pending === 0 && g.retrying === 0, 'nothing is left in progress')
    const cancelledRow = page.locator('.channel-row', { hasText: 'Cancelled' })
    check((await cancelledRow.innerText()).includes(g.cancelled.toLocaleString('en-GB')), 'the page shows the cancelled count')
    check((await page.getByRole('button', { name: 'Cancel campaign…' }).count()) === 0, 'it cannot be cancelled twice')
    await t.shot('cancelled-mid-send')
    // Nothing is sent after a cancel: TestLifecycle_CancelMidCampaign_NoDepartureAfterCancel (Go).

    // Turkish on the resend-free result page and in the drawer
    await page.getByRole('link', { name: /^SMS Cancelled/ }).click()
    await page.getByRole('dialog', { name: 'Recipient details' }).getByText('Still queued when the campaign was cancelled', { exact: false }).waitFor()
    check(true, 'the cancelled recipients are explained')

    // the same screens in Turkish
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'Türkçe' }).click()
    await page.getByText('Salt okunur sonuçlar').waitFor() // Turkish loads on first use (D58)
    check(await page.getByText('Salt okunur sonuçlar').isVisible() && await page.getByText('Gönderilmeyenler ve istisnalar').isVisible(), 'the results are in Turkish')
    await page.getByRole('link', { name: /^SMS İptal edildi/ }).click()
    await page.getByRole('dialog', { name: 'Alıcı ayrıntıları' }).waitFor()
    await t.shot('cancelled-tr')
    await page.keyboard.press('Escape')
    await page.getByRole('button', { name: 'English' }).click()
  })
