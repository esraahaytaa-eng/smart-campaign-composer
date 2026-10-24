// One-step creation (D71): languages are ticked on the audience step, and
// the AI starts writing once by itself, or not at all with "Write them myself".
import { BASE, CART_BRIEF, suite } from './lib.mjs'

export default () =>
  suite('one-step', async (t) => {
    const { page, check } = t
    const writes = [] // every AI writing request, with what it asked for
    page.on('request', (r) => r.method() === 'POST' && /\/api\/campaigns\/\d+\/content$/.test(r.url()) && writes.push(r.postDataJSON()))
    const box = (name) => page.locator('.languages').getByRole('checkbox', { name: new RegExp(name) })
    await t.signIn()

    // interpret, tick the languages, and the drafts appear without another click
    await page.goto(BASE + '/campaigns/new')
    await page.getByLabel('Campaign brief').fill(CART_BRIEF)
    await page.getByRole('button', { name: 'Interpret with AI' }).click()
    await page.waitForURL(/\/campaigns\/\d+$/)
    const id = t.track(Number(page.url().split('/').pop()))
    await page.getByText('Estimated audience').waitFor()
    check((await box('Türkçe').isChecked()) && (await box('English').isChecked()), 'the suggested languages are ticked on the audience step')
    await box('English').uncheck()
    await page.getByRole('button', { name: 'Write the messages with AI →' }).click()
    await page.locator('.ai-bar').getByText('The AI wrote the messages').waitFor({ timeout: 30000 })
    const c = await t.get(id)
    check(writes.length === 1 && writes[0].languages.join() === 'tr' && writes[0].replace === false, 'one AI request, for the ticked language only, never replacing text')
    check(Object.keys(c.content ?? {}).join() === 'tr' && (await page.locator('#sms-body').inputValue()) === c.content.tr.sms.body, 'the draft is saved and shown')
    check(!new URL(page.url()).searchParams.has('write'), 'the start signal is gone from the URL')

    await page.reload()
    await page.locator('#sms-body').waitFor()
    await page.waitForTimeout(1500)
    check(writes.length === 1, 'reloading starts no second AI request')
    await page.goBack()
    await page.getByText('Estimated audience').waitFor()
    check((await page.getByRole('link', { name: 'Write the messages →' }).isVisible()) && (await page.getByRole('button', { name: /with AI →/ }).count()) === 0, 'with text saved, the audience step just links to it')

    // "Write them myself": the same languages, no AI call
    const d = await t.draft(CART_BRIEF)
    await page.goto(`${BASE}/campaigns/${d}`)
    await page.getByText('Estimated audience').waitFor()
    await box('Türkçe').uncheck()
    await box('English').uncheck()
    const ai = page.getByRole('button', { name: 'Write the messages with AI →' })
    check((await ai.getAttribute('aria-disabled')) === 'true' && (await page.getByText('Tick at least one language').isVisible()), 'with no language ticked, the AI cannot start')
    await ai.click({ force: true }) // aria-disabled: Playwright would wait for it to be enabled
    check(new URL(page.url()).pathname === `/campaigns/${d}`, 'and clicking it stays on the audience step')
    await box('English').check()
    await page.getByRole('link', { name: 'Write them myself →' }).click()
    await page.locator('#sms-body').waitFor()
    await page.waitForTimeout(1500)
    check(writes.length === 1 && !(await t.get(d)).content, '"Write them myself" makes no AI call and saves nothing')
    check(await page.getByRole('checkbox', { name: /English/ }).isChecked(), 'the chosen language is ticked on the content step')
    check(!(await page.getByRole('checkbox', { name: /Türkçe/ }).isChecked()), 'and the unticked one is not')
  })
