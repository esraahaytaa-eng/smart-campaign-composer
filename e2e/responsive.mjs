// Layout at desktop, tablet and phone widths, and the keyboard path to
// signing out: no sideways scrolling, the page content near the top on
// small screens, the account menu in view on long pages, and every campaign
// row readable with its actions on screen.
import { BASE, CART_BRIEF, suite } from './lib.mjs'

const SIZES = [
  [1440, 900],
  [1024, 768],
  [768, 1024],
  [375, 812],
]

export default () =>
  suite('responsive', async (t) => {
    const { page, check } = t
    await t.signIn()
    const id = await t.draft(CART_BRIEF)
    await page.request.put(`${BASE}/api/campaigns/${id}/content`, {
      data: { version: 1, content: { sms: { body: 'Merhaba {{first_name}}!' }, email: { subject: 'İndirim', body: 'Merhaba {{first_name}}.' } } },
    })
    const account = page.getByRole('button', { name: /Demo Marketer/ })
    const inView = async (loc) => {
      const box = await loc.boundingBox()
      const { width, height } = page.viewportSize()
      return !!box && box.x >= 0 && box.y >= 0 && box.x + box.width <= width + 0.5 && box.y + box.height <= height + 0.5
    }

    for (const [width, height] of SIZES) {
      await page.setViewportSize({ width, height })
      for (const [name, path, ready] of [
        ['dashboard', '/', page.getByRole('heading', { name: 'Your campaigns' })],
        ['campaigns', `/campaigns?q=${encodeURIComponent('30% discount reminder')}`, page.getByText(/^Showing /)],
        ['new', '/campaigns/new', page.locator('#brief')],
        ['audience', `/campaigns/${id}`, page.getByRole('heading', { name: 'Audience rules' })],
        ['content', `/campaigns/${id}/content`, page.locator('#sms-body')],
        ['review', `/campaigns/${id}/review`, page.getByRole('heading', { name: 'When should it send?' })],
      ]) {
        await page.goto(BASE + path)
        await ready.waitFor()
        await t.shot(`${width}-${name}`)
        const sideways = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
        check(sideways <= 0, `${width}px ${name}: no sideways scrolling (${sideways}px too wide)`)
        const top = (await page.locator('main').boundingBox()).y
        check(top <= (width < 900 ? 200 : 0), `${width}px ${name}: the page starts near the top (${Math.round(top)}px down)`)
        check(await inView(account), `${width}px ${name}: the account menu is on screen`)
      }

      // campaign rows: the title readable, the actions on screen
      await page.goto(`${BASE}/campaigns?q=${encodeURIComponent('30% discount reminder')}`)
      const row = page.locator('[role=row]', { has: page.locator(`a[href="/campaigns/${id}"]`) })
      await row.waitFor()
      const title = row.locator('a.title')
      const titleWidth = (await title.boundingBox()).width
      check(titleWidth >= Math.min(240, width - 80), `${width}px: the campaign title has room (${Math.round(titleWidth)}px)`)
      for (const name of ['Delete', 'Edit']) check(await inView(row.getByRole('button', { name: new RegExp(`^${name}`) }).or(row.getByRole('link', { name: new RegExp(`^${name}`) })).first()), `${width}px: ${name} is on screen without scrolling the table`)
    }

    // a long page on a short screen: the account menu stays reachable at the top and the bottom
    await page.setViewportSize({ width: 1440, height: 600 })
    await page.goto(`${BASE}/campaigns/${id}/content`)
    await page.locator('#sms-body').waitFor()
    check(await inView(account), 'short screen, top of a long page: the account menu is on screen')
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
    check(await inView(account), 'short screen, bottom of a long page: the account menu is on screen')

    // Turkish, loaded on demand (D58): longer words, still one-line badges and no sideways scrolling
    await page.goto(BASE + '/campaigns')
    await page.getByRole('button', { name: 'Türkçe' }).click()
    await page.getByRole('heading', { name: 'Kampanyalar' }).waitFor()
    for (const [width, height] of [[1440, 900], [1024, 768], [375, 812]]) {
      await page.setViewportSize({ width, height })
      const badge = page.locator('.mode-badge')
      check((await badge.boundingBox()).height < 30, `${width}px Turkish: the mode badge stays on one line`)
      const sideways = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
      check(sideways <= 0, `${width}px Turkish: no sideways scrolling (${sideways}px too wide)`)
      await t.shot(`${width}-campaigns-tr`)
    }
    await page.getByRole('button', { name: 'English' }).click()
    await page.getByRole('heading', { name: 'Campaigns' }).waitFor()

    // signing out with the keyboard only; focus lands on the first sign-in field
    for (const [width, height] of [[1440, 900], [375, 812]]) {
      await page.setViewportSize({ width, height })
      await page.goto(BASE + '/campaigns')
      await page.getByText(/^Showing /).waitFor()
      await account.focus()
      await page.keyboard.press('Enter')
      const logout = page.getByRole('menuitem', { name: 'Log out' })
      await logout.waitFor()
      check(await inView(logout), `${width}px: the Log out item opens on screen`)
      await page.keyboard.press('ArrowDown')
      check(await logout.evaluate((el) => el === document.activeElement), `${width}px: Log out has keyboard focus`)
      await page.keyboard.press('Enter')
      await page.waitForURL(/\/login/)
      await page.getByLabel('Work email').waitFor()
      check(await page.getByLabel('Work email').evaluate((el) => el === document.activeElement), `${width}px: after signing out, focus is on Work email`)
      await t.signIn()
    }
  })
