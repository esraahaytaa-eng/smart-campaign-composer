// Campaign list paging in a real browser (D54): several pages, Next and
// Previous, a status filter and a search together, deep links and coming
// back from a campaign. Its 30 campaigns share a tag no other file uses, so
// the other lane's campaigns never land on these pages. Nothing is sent: the
// two approved campaigns are scheduled for tomorrow and cancelled at the end.
import { BASE, suite } from './lib.mjs'

export default () =>
  suite('pagination', async (t) => {
    const { page, check } = t
    await t.signIn()
    const tag = `pg${Date.now().toString(36)}`
    // Rules built by hand skip the AI (D15), so any brief text works.
    const create = async (n) => {
      const r = await page.request.post(`${BASE}/api/campaigns`, {
        data: { request: `Paging check ${tag} number ${String(n).padStart(2, '0')}`, channels: ['sms'], time_zone: 'Europe/Istanbul', rules: [{ field: 'city', op: 'in', value: ['Ankara'] }] },
      })
      return t.track((await r.json()).id)
    }
    const ids = []
    for (let n = 1; n <= 30; n++) ids.push(await create(n)) // one by one: newest first is then number 30 down to 01
    const at = new Date(Date.now() + 24 * 3600 * 1000).toISOString()
    for (const id of ids.slice(0, 2)) {
      await page.request.put(`${BASE}/api/campaigns/${id}/content`, { data: { version: 1, content: { sms: { body: 'Merhaba {{first_name}}!' } } } })
      const ok = (await page.request.post(`${BASE}/api/campaigns/${id}/approve`, { data: { version: 2, send: { at } } })).ok()
      check(ok, 'test setup: a campaign is scheduled for tomorrow')
    }

    const rows = page.locator('[role=row]:not(.header)')
    const showing = page.getByText(/^Showing /)
    const prev = page.getByRole('button', { name: 'Previous page' })
    const next = page.getByRole('button', { name: 'Next page' })
    const titles = async () => (await rows.locator('a.title').allInnerTexts()).map((s) => s.match(/number (\d+)/)?.[1])
    const shows = async (text) => {
      await page.getByText(text, { exact: true }).waitFor()
      return true
    }

    // first page of the search
    await page.goto(`${BASE}/campaigns?q=${tag}`)
    check(await shows('Showing 1–25 of 30'), 'the search finds all 30, 25 on the first page')
    const first = await titles()
    check(first.length === 25 && first[0] === '30' && first[24] === '06', `newest first on page 1 (${first[0]}…${first[24]})`)
    check(await prev.isDisabled(), 'Previous is disabled on the first page')

    // Next, then Previous
    await next.click()
    await page.waitForURL((u) => u.searchParams.get('page') === '2' && u.searchParams.get('q') === tag)
    check(await shows('Showing 26–30 of 30'), 'Next shows the rest')
    const second = await titles()
    check(second.join() === '05,04,03,02,01', `page 2 continues where page 1 ended (${second.join()})`)
    check(await next.isDisabled(), 'Next is disabled on the last page')
    await prev.click()
    await page.waitForURL((u) => !u.searchParams.has('page'))
    check(await shows('Showing 1–25 of 30'), 'Previous goes back to page 1')

    // a filter together with the search starts again at page 1
    await next.click()
    await page.waitForURL((u) => u.searchParams.get('page') === '2')
    await page.getByRole('button', { name: /^Draft\b/ }).click()
    await page.waitForURL((u) => u.searchParams.get('status') === 'draft' && !u.searchParams.has('page') && u.searchParams.get('q') === tag)
    check(await shows('Showing 1–25 of 28'), 'the Draft filter with the search starts at page 1 of 28')
    await next.click()
    check(await shows('Showing 26–28 of 28'), 'the filtered list pages too')
    check((await titles()).join() === '05,04,03', 'the scheduled campaigns are not among the drafts')
    await page.getByRole('button', { name: /^Approved\b/ }).click()
    check(await shows('Showing 1–2 of 2'), 'the Approved filter with the search shows the two scheduled campaigns')
    check((await prev.isDisabled()) && (await next.isDisabled()), 'one page: neither Previous nor Next')

    // a narrower search keeps the filter
    await page.getByRole('button', { name: /^All\b/ }).click()
    await page.getByRole('searchbox', { name: 'Search campaigns' }).fill(`${tag} number 1`)
    await page.waitForURL((u) => u.searchParams.get('q') === `${tag} number 1`)
    check(await shows('Showing 1–10 of 10'), 'a narrower search: numbers 10 to 19 only')

    // a deep link opens the page it names; coming back from a campaign returns to it
    await page.goto(`${BASE}/campaigns?q=${tag}&status=draft&page=2`)
    check(await shows('Showing 26–28 of 28'), 'a link to page 2 opens page 2')
    await rows.first().locator('a.title').click()
    await page.waitForURL((u) => /^\/campaigns\/\d+$/.test(u.pathname))
    await page.goBack()
    check(await shows('Showing 26–28 of 28'), 'Back from a campaign returns to the same page')
    await page.goto(`${BASE}/campaigns?q=${tag}&page=9`)
    check(await shows('Showing 26–30 of 30'), 'a page past the end opens the last page')

    // the pager is reachable and usable with the keyboard
    await page.goto(`${BASE}/campaigns?q=${tag}`)
    await shows('Showing 1–25 of 30')
    await next.focus()
    await page.keyboard.press('Enter')
    check(await shows('Showing 26–30 of 30'), 'Next works with the keyboard')
  })
