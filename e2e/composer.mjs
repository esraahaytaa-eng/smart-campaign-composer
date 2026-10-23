// The audience step (T16): brief → chips, editing, channels, re-interpret, delete.
import { BASE, CART_BRIEF, suite } from './lib.mjs'

export default () =>
  suite('composer', async (t) => {
    const { page, check } = t
    await t.signIn()
    await page.goto(BASE + '/campaigns/new')

    await page.getByLabel('Campaign brief').fill('Send a birthday note to users older than 40.')
    await page.getByRole('button', { name: 'Interpret with AI' }).click()
    await page.getByRole('alert').waitFor()
    check((await page.getByRole('alert').textContent()).includes("couldn't turn this brief"), 'an unsupported brief is explained')
    check(page.url().endsWith('/campaigns/new'), 'and nothing is created')

    await page.getByLabel('Campaign brief').fill(CART_BRIEF)
    await page.getByRole('button', { name: 'Interpret with AI' }).click()
    await page.waitForURL(/\/campaigns\/\d+$/)
    const id = t.track(Number(page.url().split('/').pop()))
    await page.getByText('Estimated audience').waitFor()
    const chips = (await page.locator('.chip-label').allTextContents()).map((c) => c.replace('. Edit', ''))
    check(chips.includes('City is İstanbul'), 'the city rule reads as a sentence')
    check(chips.includes('Added to cart in the last 30 days (counted from send time)'), 'a time rule says it counts from send time')
    check((await page.getByRole('heading', { level: 1 }).textContent()) === 'Send a 30% discount reminder to users in Istanbul who added…', 'the title is the start of the brief (D43)')
    await t.shot('audience')

    const dialog = page.getByRole('dialog')
    await page.locator('.chip-label', { hasText: 'Added to cart' }).click()
    await dialog.getByLabel('Number of days').fill('14')
    await dialog.getByRole('button', { name: 'Apply' }).click()
    await dialog.waitFor({ state: 'detached' })
    check((await page.locator('.chip-label').allTextContents()).some((c) => c.startsWith('Added to cart in the last 14 days')), 'editing a rule updates its chip')

    await page.locator('.chip-label', { hasText: 'Added to cart' }).click()
    await dialog.getByLabel('Number of days').fill('0')
    await dialog.getByRole('button', { name: 'Apply' }).click()
    check(await dialog.getByText('Enter 1 to 365 days.').isVisible(), 'an invalid value is caught in the editor')
    await dialog.getByRole('button', { name: 'Cancel' }).click()

    await page.locator('.chip-label', { hasText: 'Added to cart' }).click()
    await dialog.locator('input[type=radio][value=since]').check()
    await dialog.getByLabel('Start date').fill('2026-10-01')
    await dialog.getByRole('button', { name: 'Apply' }).click()
    await dialog.waitFor({ state: 'detached' })
    check((await page.locator('.chip-label').allTextContents()).some((c) => c.startsWith('Added to cart since 1 Oct 2026')), 'a fixed-date rule reads "since 1 Oct 2026"')

    await page.getByRole('button', { name: '+ Add rule' }).click()
    await page.getByRole('menuitem', { name: 'Number of orders: at least' }).click()
    await dialog.getByRole('button', { name: 'Apply' }).click()
    await dialog.waitFor({ state: 'detached' })
    check((await page.locator('.chip-label').allTextContents()).some((c) => c.startsWith('At least 1 order')), 'a rule can be added from the catalog')

    await page.getByRole('button', { name: 'SMS', exact: true }).click()
    await page.getByText('All changes saved').waitFor()
    check(await page.getByText('Email not selected').isVisible(), 'channels can be changed')

    await page.getByRole('button', { name: 'Re-interpret brief' }).click()
    check(await page.getByRole('alertdialog').isVisible(), 're-interpreting asks first')
    await page.getByRole('button', { name: 'Re-interpret' }).click()
    await page.getByText('All changes saved').waitFor()
    check((await page.locator('.chip-label').allTextContents()).some((c) => c.startsWith('Added to cart in the last 30 days')), "re-interpreting restores the AI's rules")

    await page.getByRole('button', { name: 'Delete draft…' }).click()
    await page.getByRole('button', { name: 'Delete permanently' }).click()
    await page.waitForURL(/\/campaigns$/)
    check((await page.request.get(`${BASE}/api/campaigns/${id}`)).status() === 404, 'deleting a draft removes it')
  })
