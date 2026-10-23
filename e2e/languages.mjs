// English and Turkish (D44): the switch, persistence, and data left untouched.
import { BASE, CART_BRIEF, suite } from './lib.mjs'

export default () =>
  suite('languages', async (t) => {
    const { page, check } = t
    await page.goto(BASE + '/login')
    await page.getByRole('heading', { name: 'Sign in' }).waitFor()
    check((await page.evaluate(() => document.documentElement.lang)) === 'en', 'English by default')

    await page.getByRole('button', { name: 'Türkçe' }).click()
    await page.getByRole('heading', { name: 'Giriş yap' }).waitFor()
    check((await page.evaluate(() => document.documentElement.lang)) === 'tr', '<html lang> follows the language')
    check((await page.title()) === 'Giriş · Smart Campaign Composer', 'the tab title follows the language')
    await page.reload()
    await page.getByRole('heading', { name: 'Giriş yap' }).waitFor()
    check(true, 'the choice survives a reload')

    await page.getByRole('button', { name: 'Giriş yap' }).click()
    check(await page.getByText('İş e-postanızı girin.').isVisible(), 'field errors in Turkish')
    await page.getByLabel('İş e-postası').fill('marketer@marketer.com')
    await page.getByLabel('Şifre', { exact: true }).fill(process.env.DEMO_PASSWORD ?? 'Demo1234!')
    await page.getByRole('button', { name: 'Giriş yap' }).click()
    await page.getByRole('heading', { name: 'Panel' }).waitFor()
    check(await page.getByText('Simüle YZ ve sağlayıcılar').isVisible(), 'the mode badge in Turkish')

    await page.getByRole('navigation', { name: 'Ana menü' }).getByRole('link', { name: '+ Yeni kampanya' }).click()
    const odd = 'Send a birthday note to users older than 40.'
    await page.getByLabel('Kampanya açıklaması').fill(odd)
    await page.getByRole('button', { name: 'Yapay zekâ ile yorumla' }).click()
    await page.getByRole('alert').waitFor()
    const reason = await page.getByRole('alert').locator('q').textContent()
    const api = (await (await page.request.post(BASE + '/api/campaigns', { data: { request: odd, channels: ['sms'], time_zone: 'Europe/Istanbul' } })).json()).error
    check(reason === api, "the AI's reason is shown exactly as returned")

    await page.getByLabel('Kampanya açıklaması').fill(CART_BRIEF)
    await page.getByRole('button', { name: 'Yapay zekâ ile yorumla' }).click()
    await page.waitForURL(/\/campaigns\/\d+$/)
    t.track(Number(page.url().split('/').pop()))
    await page.getByText('Tahmini hedef kitle').waitFor()
    const chips = (await page.locator('.chip-label').allTextContents()).map((c) => c.replace(/\. Düzenle$/, ''))
    check(chips.includes('Şehir: İstanbul'), 'chips in Turkish, city name unchanged')
    check((await page.locator('#brief').inputValue()) === CART_BRIEF, 'the brief is shown exactly as written')
    await t.shot('composer-tr')

    await page.getByRole('button', { name: 'English' }).click()
    await page.getByText('Estimated audience').waitFor()
    check((await page.locator('.chip-label').allTextContents()).some((c) => c.startsWith('City is İstanbul')), 'switching back is instant')
    check((await page.evaluate(() => localStorage.getItem('scc.locale'))) === 'en', 'the choice is stored')
  })
