// Autosave and races on the audience step (T16 review): quick edits, campaign
// switching, failures and changes made elsewhere never save wrong data.
import { BASE, CART_BRIEF, suite } from './lib.mjs'

const keys = (c) => (c.rules ?? []).map((r) => `${r.field} ${r.op}`).sort()
const slow = (ms, fail = false) => async (route) => {
  await new Promise((r) => setTimeout(r, ms))
  if (fail) return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"internal error"}' })
  return route.continue()
}

export default () =>
  suite('saving', async (t) => {
    const { page, check } = t
    const open = async (id) => {
      await page.goto(`${BASE}/campaigns/${id}`)
      await page.getByText('Estimated audience').waitFor()
    }
    const remove = (text) => page.getByRole('button', { name: new RegExp('^Remove rule: ' + text) })
    await t.signIn()

    // removing a channel that has content asks first, then deletes only that content
    {
      const id = await t.draft(CART_BRIEF)
      await page.request.put(`${BASE}/api/campaigns/${id}/content`, {
        data: { version: 1, content: { sms: { body: 'Merhaba {{first_name}}!' }, email: { subject: 'İndirim', body: 'Merhaba {{first_name}}.' } } },
      })
      await open(id)
      await page.getByRole('button', { name: 'SMS', exact: true }).click()
      const dlg = page.getByRole('alertdialog')
      check((await dlg.textContent()).includes('Remove email?'), 'removing email with content asks first')
      await dlg.getByRole('button', { name: 'Remove and delete content' }).click()
      await page.getByText('All changes saved').waitFor()
      const after = await t.get(id)
      check(JSON.stringify(after.channels) === '["sms"]' && after.content.sms && !after.content.email, 'only the removed channel loses its content')
    }

    // two quick removals while saves are slow
    {
      const id = await t.draft(CART_BRIEF)
      await open(id)
      await page.route('**/api/campaigns/*/rules', slow(500))
      await remove('No order').click()
      await remove('Added to cart').click()
      await page.getByText('All changes saved').waitFor({ timeout: 10000 })
      await page.waitForTimeout(800)
      await page.unroute('**/api/campaigns/*/rules')
      check(JSON.stringify(keys(await t.get(id))) === '["city in"]', 'both quick removals stick')
    }

    // switching campaigns while a save is still running
    {
      const a = await t.draft(CART_BRIEF)
      const b = await t.draft(CART_BRIEF)
      await open(a)
      await page.route('**/api/campaigns/*/rules', slow(1500))
      await remove('No order').click()
      await page.evaluate((p) => document.querySelector('#app').__vue_app__.config.globalProperties.$router.push(p), `/campaigns/${b}`)
      await page.getByText('Estimated audience').waitFor()
      await page.waitForTimeout(2500)
      await page.unroute('**/api/campaigns/*/rules')
      const A = await t.get(a)
      const B = await t.get(b)
      check(!keys(A).includes('no_order within'), "a queued save lands on its own campaign")
      check(B.version === 1 && keys(B).length === 3, 'the other campaign is untouched')
      check(!(await page.locator('.change').isVisible()) && !(await page.getByRole('alert').isVisible()), 'no state carries over')
    }

    // a failure after the editor was closed is shown, nothing saved
    {
      const id = await t.draft(CART_BRIEF)
      await open(id)
      await page.route('**/api/campaigns/*/rules', slow(600, true))
      await page.locator('.chip-label', { hasText: 'Added to cart' }).click()
      await page.getByRole('dialog').getByLabel('Number of days').fill('9')
      await page.getByRole('dialog').getByRole('button', { name: 'Apply' }).click()
      await page.keyboard.press('Escape')
      await page.waitForTimeout(1200)
      await page.unroute('**/api/campaigns/*/rules')
      check((await page.getByRole('alert').allTextContents()).some((x) => x.includes('The server ran into a problem')), 'a failure after closing the editor is shown')
      check((await t.get(id)).rules.find((r) => r.field === 'cart_added')?.value?.last_days === 30, 'and nothing is saved')
    }

    // a change made elsewhere locks the page
    {
      const id = await t.draft(CART_BRIEF)
      await open(id)
      await page.request.put(`${BASE}/api/campaigns/${id}/channels`, { data: { version: 1, channels: ['sms'] } })
      await remove('No order').click()
      await page.getByText('This draft was changed elsewhere').waitFor()
      const after = await t.get(id)
      check(after.version === 2 && keys(after).length === 3, 'a stale edit is refused; the other change stands')
    }

    // the last rule can't be removed, and says why
    {
      const id = await t.draft(CART_BRIEF)
      await page.request.put(`${BASE}/api/campaigns/${id}/rules`, { data: { version: 1, rules: [{ field: 'city', op: 'in', value: ['İstanbul'] }] } })
      await open(id)
      const btn = remove('City')
      check((await btn.getAttribute('aria-disabled')) === 'true', 'the last remove button is disabled for clicks but focusable')
      check(await page.getByText('A campaign needs at least one audience rule.').isVisible(), 'the reason is shown')
      await btn.click({ force: true })
      await page.waitForTimeout(400)
      check((await t.get(id)).version === 2, 'clicking it saves nothing')
    }
  })
