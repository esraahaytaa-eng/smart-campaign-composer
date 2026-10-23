// Resend (T18 step 7, D31, D48, boards 6 and B): a campaign whose SMS was too
// long for some names offers a resend; the dialog explains who gets it; the
// follow-up goes through the content and review steps and reaches only the
// skipped recipients. Sends to the Ankara SMS customers through the mock
// provider (the original and the resend).
import { ANKARA_BRIEF, BASE, suite } from './lib.mjs'

const LONG = 'Merhaba {{first_name}} {{last_name}}, ' + 'hafta sonu indirimi başladı! Sepetinizdeki ürünlerde geçerli fırsatlar için hemen uygulamayı açın ve kodu girin: HAFTA'.padEnd(150 - 'Merhaba , '.length, '.')
const SHORT = 'Merhaba {{first_name}}, hafta sonu indirimi başladı! Kod: HAFTA'

export default () =>
  suite('resend', async (t) => {
    const { page, check } = t
    await t.signIn()
    const done = async (id) => {
      for (let i = 0; i < 120 && (await t.get(id)).status !== 'completed'; i++) await page.waitForTimeout(1000)
    }

    const id = await t.draft(ANKARA_BRIEF, ['sms'])
    await page.request.put(`${BASE}/api/campaigns/${id}/content`, { data: { version: 1, content: { sms: { body: LONG } } } })
    check((await page.request.post(`${BASE}/api/campaigns/${id}/approve`, { data: { version: 2, send: 'now' } })).ok(), 'test setup: a too-long SMS is sent to Ankara')
    await done(id)
    const groups = (await (await page.request.get(`${BASE}/api/campaigns/${id}/recipients?group=skipped`)).json()).groups.sms

    // the offer and the dialog
    await page.goto(`${BASE}/campaigns/${id}`)
    const offer = page.getByRole('button', { name: /^Resend to ~\d+/ })
    await offer.waitFor()
    const eligible = Number((await offer.innerText()).replace(/\D/g, ''))
    check(eligible > 0 && eligible <= groups.skipped, `the offer names how many can be resent (${eligible} of ${groups.skipped} skipped)`)
    await offer.click()
    const dlg = page.getByRole('dialog', { name: 'Resend a shorter SMS' })
    await dlg.waitFor()
    check((await dlg.locator('.funnel').innerText()).includes(String(groups.skipped)), 'the dialog starts from the skipped count')
    check(await dlg.getByText('Uncertain — never resent').isVisible(), 'the dialog says uncertain recipients are left out')
    await t.shot('resend-dialog')
    await dlg.getByRole('button', { name: 'Go back' }).click()

    // the drawer's Skipped tab offers it too
    await page.goto(`${BASE}/campaigns/${id}?recipients=skipped&channel=sms`)
    await page.getByRole('dialog', { name: 'Recipient details' }).getByRole('button', { name: /Resend a shorter SMS to the character-limit skips/ }).click()
    await dlg.waitFor()
    check(!new URL(page.url()).searchParams.has('recipients'), 'the drawer closes when the resend dialog opens')

    // create it, write a shorter SMS, approve
    await dlg.getByRole('button', { name: 'Create the resend draft' }).click()
    await page.waitForURL((u) => /\/campaigns\/\d+\/content$/.test(u.pathname))
    const resend = t.track(Number(new URL(page.url()).pathname.split('/')[2]))
    const created = await t.get(resend)
    check(created.parent_campaign_id === id && created.channels.join() === 'sms', 'the resend is a draft linked to the campaign, SMS only')
    await page.getByText('Show original SMS').click()
    check(await page.getByText(LONG.slice(0, 40), { exact: false }).isVisible(), 'the original SMS can be shown')
    await page.locator('#sms-body').fill(SHORT)
    await page.locator('#sms-body').blur()
    await page.getByText('All changes saved').waitFor()
    await page.goto(`${BASE}/campaigns/${resend}/review`)
    await page.getByRole('radio', { name: /Send now/ }).click()
    await page.getByRole('checkbox').check()
    await page.getByRole('button', { name: /^Approve and send/ }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: /^Approve and send/ }).click()
    await page.waitForURL((u) => u.pathname === `/campaigns/${resend}`)
    await done(resend)
    const r = await (await page.request.get(`${BASE}/api/campaigns/${resend}/recipients?group=sent`)).json()
    const total = Object.values(r.groups.sms).reduce((a, b) => a + b, 0)
    // The mock provider may fail a few by design, so the audience is what is checked.
    check(total === eligible && r.groups.sms.skipped === 0, `the resend's audience is exactly the eligible skipped recipients (${eligible}), none too long again`)
    check(await page.getByText('This is a resend of', { exact: false }).isVisible(), "the resend's page links to the original")

    // the original now points to the resend and offers no second one
    await page.goto(`${BASE}/campaigns/${id}`)
    await page.getByRole('link', { name: 'Open the resend' }).waitFor()
    check((await offer.count()) === 0, 'a second resend is not offered')
    await page.goto(`${BASE}/campaigns?q=${encodeURIComponent('weekend sale announcement')}`)
    const row = page.locator('[role=row]', { has: page.locator(`a[href="/campaigns/${resend}"]`) })
    check((await row.getByRole('link', { name: /^Resend of/ }).getAttribute('href')) === `/campaigns/${id}`, 'the list links the resend to its original')

    // a conflict: the resend is created elsewhere while the dialog is open
    const other = await t.draft(ANKARA_BRIEF, ['sms'])
    await page.request.put(`${BASE}/api/campaigns/${other}/content`, { data: { version: 1, content: { sms: { body: LONG } } } })
    await page.request.post(`${BASE}/api/campaigns/${other}/approve`, { data: { version: 2, send: 'now' } })
    await done(other)
    await page.goto(`${BASE}/campaigns/${other}`)
    await offer.click()
    await dlg.waitFor()
    const elsewhere = await page.request.post(`${BASE}/api/campaigns/${other}/resend`, { data: { channel: 'sms' } })
    t.track((await elsewhere.json()).id)
    await dlg.getByRole('button', { name: 'Create the resend draft' }).click()
    await dlg.getByText('already has an active resend', { exact: false }).waitFor()
    check(true, 'a resend created elsewhere is reported, not duplicated')
    await dlg.getByRole('button', { name: 'Go back' }).click()
    await page.getByRole('link', { name: 'Open the resend' }).waitFor()
    check(true, 'the page then points to the existing resend')
  })
