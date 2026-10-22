// Sign-in, the login guard and the app shell (T15).
import { BASE, suite } from './lib.mjs'

export default () =>
  suite('sign-in', async (t) => {
    const { page, check } = t
    await page.goto(BASE + '/campaigns/new')
    await page.waitForURL(/\/login\?redirect=/)
    check(decodeURIComponent(page.url()).includes('redirect=/campaigns/new'), 'a signed-out deep link goes to sign-in with the return address')
    await t.shot('sign-in')

    await page.getByRole('button', { name: 'Sign in' }).click()
    check(await page.getByText('Enter your work email.').isVisible(), 'an empty email is reported')
    check((await page.evaluate(() => document.activeElement?.id)) === 'email', 'focus moves to the email field')

    await page.getByLabel('Work email').fill('marketer@marketer.com')
    await page.getByLabel('Password', { exact: true }).fill('wrong-password')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await page.getByRole('alert').waitFor()
    check((await page.getByRole('alert').textContent()).includes('Email or password is incorrect'), 'a wrong password is reported')
    check((await page.evaluate(() => document.activeElement?.tagName)) === 'INPUT', 'focus stays on a field after a failed sign-in')

    await page.getByLabel('Password', { exact: true }).fill(process.env.DEMO_PASSWORD ?? 'Demo1234!')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await page.getByRole('heading', { name: 'New campaign' }).waitFor()
    check(true, 'signing in returns to the requested page')
    check(await page.getByText('Mock AI · Mock providers').isVisible(), 'the AI mode is shown')

    await page.keyboard.press('Tab')
    check((await page.evaluate(() => getComputedStyle(document.activeElement).outlineStyle)) === 'solid', 'keyboard focus has a solid outline')

    await page.getByRole('button', { name: /Demo Marketer/ }).click()
    await page.getByRole('menuitem', { name: 'Log out' }).click()
    await page.waitForURL(/\/login/)
    check(true, 'log out returns to sign-in')
  })
