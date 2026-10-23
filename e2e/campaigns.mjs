// Dashboard and campaign list (T18 step 4, boards B and C): status counts,
// filters and search in the URL, row actions, delete and cancel with a
// confirmation, Turkish.
import { ANKARA_BRIEF, BASE, CART_BRIEF, suite } from './lib.mjs'

const content = { sms: { body: 'Merhaba {{first_name}}, hafta sonu indirimi başladı!' }, email: { subject: 'İndirim', body: 'Merhaba {{first_name}}.' } }

export default () =>
  suite('campaigns', async (t) => {
    const { page, check } = t
    await t.signIn()
    const row = (id) => page.locator('[role=row]', { has: page.locator(`a[href="/campaigns/${id}"]`) })

    // a draft, and a campaign scheduled for tomorrow (cancelled at the end, nothing is sent)
    const draft = await t.draft(CART_BRIEF)
    const scheduled = await t.draft(ANKARA_BRIEF, ['sms'])
    await page.request.put(`${BASE}/api/campaigns/${scheduled}/content`, { data: { version: 1, content: { sms: content.sms } } })
    const at = new Date(Date.now() + 24 * 3600 * 1000).toISOString()
    const approved = await page.request.post(`${BASE}/api/campaigns/${scheduled}/approve`, { data: { version: 2, send: { at } } })
    check(approved.ok(), 'test setup: a campaign is scheduled for tomorrow')
    const list = await (await page.request.get(`${BASE}/api/campaigns`)).json()
    const count = (s) => list.filter((c) => c.status === s).length

    // dashboard
    await page.goto(BASE + '/')
    await page.getByRole('heading', { name: 'Your campaigns' }).waitFor()
    for (const [label, status] of [['Drafts', 'draft'], ['Scheduled', 'approved'], ['Sending', 'sending'], ['Completed', 'completed'], ['Cancelled', 'cancelled']]) {
      const card = page.getByRole('link', { name: new RegExp(`^${label}\\b`) })
      check((await card.innerText()).includes(String(count(status))), `the ${label} card shows ${count(status)}`)
    }
    const dashRows = page.locator('.dash-row')
    check((await dashRows.count()) <= 6, 'the dashboard shows at most six campaigns')
    const firstDraftOrLive = await dashRows.first().innerText()
    check(/Draft|Sending/.test(firstDraftOrLive), "what's live or waiting comes first")
    await t.shot('dashboard')
    await page.getByRole('link', { name: /^Drafts\b/ }).click()
    await page.waitForURL((u) => u.pathname === '/campaigns' && u.searchParams.get('status') === 'draft')
    check((await page.getByRole('button', { name: /^Draft\b/ }).getAttribute('aria-pressed')) === 'true', 'a status card opens the list filtered by it')

    // list: filter, search, URL
    await page.getByRole('row').nth(1).waitFor()
    check(await row(draft).isVisible(), 'the new draft is listed')
    check(!(await row(scheduled).isVisible()), 'the draft filter hides the scheduled campaign')
    await page.getByRole('button', { name: /^All\b/ }).click()
    await page.getByRole('searchbox', { name: 'Search campaigns' }).fill('ANKARA HAFTA')
    check(!(await row(draft).isVisible()), 'search narrows the list')
    await page.getByRole('searchbox', { name: 'Search campaigns' }).fill('WEEKEND SALE ANNOUNCEMENT')
    await page.waitForURL((u) => u.searchParams.get('q') === 'WEEKEND SALE ANNOUNCEMENT')
    check(await row(scheduled).isVisible(), 'search finds the brief (case-insensitive)')
    await page.reload()
    await row(scheduled).waitFor() // the list loads after the page
    check(await row(scheduled).isVisible() && (await page.getByRole('searchbox', { name: 'Search campaigns' }).inputValue()) === 'WEEKEND SALE ANNOUNCEMENT',
      'the search survives a reload')
    check((await row(scheduled).innerText()).includes('Approved'), 'a scheduled campaign shows its status')
    check(/\d{1,2} \w{3} 2026/.test(await row(scheduled).locator('[role=cell]').nth(4).innerText()), 'the scheduled column shows the send time')
    await page.getByRole('searchbox', { name: 'Search campaigns' }).fill('no campaign matches this text zzz')
    await page.getByText('No campaigns match.').waitFor()
    await page.getByRole('button', { name: 'Clear search and filters' }).click()
    check(await row(draft).isVisible(), 'clearing brings every campaign back')
    await t.shot('campaigns')

    // delete a draft
    await row(draft).getByRole('button', { name: /^Delete/ }).click()
    const dlg = page.getByRole('alertdialog')
    check(await dlg.getByText('This can’t be undone.', { exact: false }).isVisible(), 'deleting asks first')
    await dlg.getByRole('button', { name: 'Keep draft' }).click()
    check(await row(draft).isVisible(), 'keeping the draft changes nothing')
    await row(draft).getByRole('button', { name: /^Delete/ }).click()
    await dlg.getByRole('button', { name: 'Delete permanently' }).click()
    await row(draft).waitFor({ state: 'detached' })
    check((await page.request.get(`${BASE}/api/campaigns/${draft}`)).status() === 404, 'the draft is deleted')

    // cancel the scheduled campaign
    await row(scheduled).getByRole('button', { name: /^Cancel/ }).click()
    check(await dlg.getByText('It won’t send at', { exact: false }).isVisible(), 'cancelling a scheduled campaign says it won’t send')
    await dlg.getByRole('button', { name: 'Cancel campaign' }).click()
    await row(scheduled).getByText('Cancelled').waitFor()
    check((await t.get(scheduled)).status === 'cancelled', 'the campaign is cancelled')
    check((await row(scheduled).getByRole('link', { name: 'View results' }).count()) === 1, 'a cancelled campaign offers its results')

    // Turkish
    await page.goto(BASE + '/')
    await page.getByRole('button', { name: 'Türkçe' }).click()
    await page.getByRole('heading', { name: 'Kampanyalarınız' }).waitFor()
    check(await page.getByRole('link', { name: /^Taslaklar\b/ }).isVisible(), 'the dashboard is in Turkish')
    await page.getByRole('button', { name: 'English' }).click()
  })
