// Recipient details (T18 step 6, board 5): the drawer opened from the
// delivery page, its tabs, search, paging, attempt history, masked contacts,
// Back and Escape, Turkish. Sends one campaign to the Ankara SMS customers
// through the mock provider, with an SMS that is too long for longer names,
// so some recipients are sent and some skipped.
import { BASE, longSmsToAnkara, suite } from './lib.mjs'

export default () =>
  suite('recipients', async (t) => {
    const { page, check } = t
    await t.signIn()

    const { id, ok } = await longSmsToAnkara(t) // shared with resend.mjs
    check(ok, 'test setup: a too-long SMS was sent to Ankara and finished')
    const api = async (q) => (await page.request.get(`${BASE}/api/campaigns/${id}/recipients?${q}`)).json()
    const groups = (await api('group=sent')).groups.sms
    check(groups.sent > 0 && groups.skipped > 0, `test setup: both sent (${groups.sent}) and skipped (${groups.skipped})`)

    // open from the delivery page
    await page.goto(`${BASE}/campaigns/${id}`)
    await page.getByText('Read-only results').waitFor()
    const skippedRow = page.getByRole('link', { name: /^SMS Skipped/ })
    await skippedRow.click()
    const drawer = page.getByRole('dialog', { name: 'Recipient details' })
    await drawer.waitFor()
    check(new URL(page.url()).searchParams.get('recipients') === 'skipped', 'the drawer is in the address')
    const tab = (name) => drawer.getByRole('tab', { name: new RegExp(`^${name}`) })
    check((await tab('Skipped').getAttribute('aria-selected')) === 'true', 'the clicked group is selected')
    check((await tab('Skipped').innerText()).includes(String(groups.skipped)), 'the tab shows the API count')
    check(await drawer.getByText('Skipped — never attempted').isVisible(), 'the reason box explains the group')
    const rows = drawer.locator('[role=row]:not(.header)')
    await rows.first().waitFor()
    check(/\d+ \/ 160 characters/.test(await rows.first().innerText()), 'a length skip shows how long the SMS was')
    const contacts = await drawer.locator('.contact').allInnerTexts()
    check(contacts.length > 0 && contacts.every((c) => /^\+90 \d{3} ••• •• \d{2}$/.test(c)), 'phone numbers are masked')
    await t.shot('recipients')

    // sent: attempt history
    await tab('Sent').click()
    await page.waitForURL((u) => u.searchParams.get('recipients') === 'sent')
    await drawer.getByText('Sent — accepted by the mock provider').waitFor()
    const expand = drawer.getByRole('button', { name: /^Show attempt history for/ }).first()
    await expand.click()
    check((await expand.getAttribute('aria-expanded')) === 'true', 'the history opens')
    check(await drawer.getByText('Attempt 1').first().isVisible(), 'the attempts are listed')
    check(await drawer.getByText('Accepted by the mock provider → Sent', { exact: false }).first().isVisible(), 'the last attempt says it was sent')

    // paging (the sent group has more than one page when over 50)
    if (groups.sent > 50) {
      check(await drawer.getByText(`Showing 1–50 of ${groups.sent}`, { exact: false }).isVisible(), 'the footer says which rows are shown')
      await drawer.getByRole('button', { name: 'Next page' }).click()
      await drawer.getByText(`Showing 51–`, { exact: false }).waitFor()
      check(new URL(page.url()).searchParams.get('page') === '2', 'the page is in the address')
    }

    // search
    const first = (await rows.first().locator('.name').innerText()).split(' ')[0]
    await drawer.getByRole('searchbox', { name: 'Search recipients by name' }).fill(first.toLocaleUpperCase('tr'))
    await page.waitForURL((u) => u.searchParams.get('q') === first.toLocaleUpperCase('tr'))
    await page.waitForTimeout(300)
    const found = await drawer.locator('.name').allInnerTexts()
    check(found.length > 0 && found.every((n) => n.toLocaleLowerCase('tr').includes(first.toLocaleLowerCase('tr'))), `search finds "${first}" ignoring case`)
    await drawer.getByRole('searchbox', { name: 'Search recipients by name' }).fill('zzzz')
    await drawer.getByText('No recipients match “zzzz”.').waitFor()
    check(true, 'a search without matches says so')

    // Escape closes and gives focus back; Back closes too
    await page.keyboard.press('Escape')
    await drawer.waitFor({ state: 'detached' })
    check(!new URL(page.url()).searchParams.has('recipients'), 'closing removes the drawer from the address')
    check((await page.evaluate(() => document.activeElement?.getAttribute('aria-label') ?? '')).startsWith('SMS Skipped'), 'focus returns to the row that opened it')
    await skippedRow.click()
    await drawer.waitFor()
    await page.goBack()
    await drawer.waitFor({ state: 'detached' })
    check(new URL(page.url()).pathname === `/campaigns/${id}`, 'Back closes the drawer and stays on the campaign')

    // a link opens it directly; Turkish
    await page.goto(`${BASE}/campaigns/${id}?recipients=skipped&channel=sms`)
    await drawer.waitFor()
    await page.evaluate(() => localStorage.setItem('scc.locale', 'tr'))
    await page.reload()
    await page.getByRole('dialog', { name: 'Alıcı ayrıntıları' }).waitFor()
    check(await page.getByRole('tab', { name: /^Atlandı/ }).isVisible(), 'the drawer is in Turkish')
    await page.evaluate(() => localStorage.setItem('scc.locale', 'en'))
  })
