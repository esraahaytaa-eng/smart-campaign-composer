// Content and review & approve (T17), including the T17 review fixes: no
// lost edits, safe navigation, version conflicts, and explicit approval.
import { ANKARA_BRIEF, BASE, CART_BRIEF, suite, WELCOME_BRIEF, putContent } from './lib.mjs'

const SMS = 'Merhaba {{first_name}}, hafta sonu indirimi başladı!'
const content = (sms = SMS) => ({ sms: { body: sms }, email: { subject: 'İndirim', body: 'Merhaba {{first_name}}.' } })

export default () =>
  suite('content-review', async (t) => {
    const { page, check } = t
    const bar = page.locator('.ai-bar')
    await t.signIn()

    // streaming, Stop, autosave, variables, fictional preview
    const id = await t.draft(ANKARA_BRIEF)
    await page.goto(`${BASE}/campaigns/${id}/content`)
    await page.getByText('No messages yet').waitFor()
    await page.getByRole('button', { name: 'Write with AI' }).click()
    await bar.getByText('The AI is writing the messages…').waitFor()
    check((await page.locator('#sms-body').getAttribute('readonly')) !== null, 'fields are read-only while the AI writes')
    await bar.getByText('The AI wrote the messages').waitFor({ timeout: 30000 })
    const written = await t.get(id)
    check(!!written.content?.tr?.sms?.body && (await page.locator('#sms-body').inputValue()) === written.content.tr.sms.body, "the AI's messages are saved and shown")
    const preview = page.locator('section', { has: page.getByRole('heading', { name: 'Preview' }) })
    check(await preview.getByText('Fictional examples: no real customer data is shown.').isVisible(), 'the preview says its customers are fictional')
    check(await preview.getByText(/segment/).isVisible(), 'SMS encoding and segments are shown')
    await t.shot('content')

    await page.locator('#sms-body').fill(SMS + ' ')
    await page.locator('#sms-body').press('End')
    await page.getByRole('button', { name: 'Insert city' }).first().click()
    await page.locator('#email-subject').click()
    await page.getByText('All changes saved').waitFor()
    await page.waitForTimeout(400)
    const edited = await t.get(id)
    check(edited.content.tr.sms.body === SMS + ' {{city}}', 'an edit with an inserted variable is saved')

    await page.getByRole('button', { name: /^Rewrite .+ with AI$/ }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Rewrite' }).click()
    await page.getByRole('button', { name: 'Stop' }).click()
    await bar.getByText('Stopped. Nothing was saved').waitFor()
    const stopped = await t.get(id)
    check(stopped.version === edited.version && stopped.content.tr.sms.body === edited.content.tr.sms.body, 'Stop saves nothing')

    // Reach the content step the way a marketer does: an in-app link, so Back
    // is a route change the app guards (a full page load would use the
    // browser's own leave prompt, checked further down).
    const viaApp = async (d) => {
      await page.goto(`${BASE}/campaigns/${d}`)
      await page.getByText('Estimated audience').waitFor()
      await page.getByRole('link', { name: 'Write the messages →' }).click()
      await page.locator('#sms-body').waitFor()
    }

    // review fix 1: text typed but not saved yet is never replaced without asking
    {
      const d = await t.draft(WELCOME_BRIEF, ['sms', 'email'])
      await viaApp(d)
      await page.locator('#sms-body').fill('My own SMS text')
      await page.getByRole('button', { name: 'Write with AI' }).click()
      check(await page.getByRole('alertdialog').isVisible(), 'writing with AI over unsaved typing asks first')
      await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
      check((await page.locator('#sms-body').inputValue()) === 'My own SMS text', 'cancelling keeps the typed text')

      // review fix 2: leaving with incomplete messages asks; "stay" keeps the text
      let asked = ''
      page.once('dialog', (dlg) => {
        asked = dlg.message()
        dlg.dismiss()
      })
      await page.evaluate(() => history.back()) // "stay" cancels the navigation, so don't wait for one
      await page.waitForTimeout(800)
      check(asked.includes('still empty'), 'leaving with incomplete messages asks first')
      check(page.url().endsWith(`/campaigns/${d}/content`), 'and "stay" keeps the marketer on the page')
      check((await page.locator('#sms-body').inputValue()) === 'My own SMS text', 'with the typed text intact')
      await page.locator('#sms-body').fill('') // nothing unsaved before moving on
    }

    // review fix 2: Back right after typing saves the edit (no blur needed)
    {
      const d = await t.draft(CART_BRIEF)
      await putContent(page, d, 1, content())
      await viaApp(d)
      await page.locator('#sms-body').fill('Typed and left with Back {{first_name}}')
      await page.goBack()
      await page.getByText('Estimated audience').waitFor()
      check((await t.get(d)).content.tr.sms.body === 'Typed and left with Back {{first_name}}', 'Back after typing saves the edit first')

      // closing the tab with unsaved typing triggers the browser's leave prompt
      const tab = await t.context.newPage()
      await tab.goto(`${BASE}/campaigns/${d}/content`)
      await tab.locator('#sms-body').waitFor()
      await tab.locator('#sms-body').fill('Unsaved when closing')
      let prompted = false
      tab.on('dialog', (dlg) => {
        prompted = dlg.type() === 'beforeunload'
        dlg.dismiss()
      })
      await tab.close({ runBeforeUnload: true })
      await page.waitForTimeout(500)
      check(prompted, 'closing the tab with unsaved changes asks the browser to confirm')
    }

    // review fix 3: a change elsewhere during generation is a conflict, not adopted
    {
      const d = await t.draft(CART_BRIEF)
      await putContent(page, d, 1, content())
      await page.goto(`${BASE}/campaigns/${d}/content`)
      await page.locator('#sms-body').waitFor()
      await page.getByRole('button', { name: /^Rewrite .+ with AI$/ }).click()
      await page.getByRole('alertdialog').getByRole('button', { name: 'Rewrite' }).click()
      await bar.getByText('The AI is writing the messages…').waitFor()
      await putContent(page, d, 2, content('OTHER TAB EDIT'))
      await page.getByText('This draft was changed elsewhere').waitFor({ timeout: 30000 })
      check(true, 'a change elsewhere during generation shows "changed elsewhere"')
      check((await t.get(d)).content.tr.sms.body === 'OTHER TAB EDIT', "the other tab's edit is kept")
      check((await page.locator('#sms-body').getAttribute('readonly')) !== null, 'editing stays locked until reload')
    }

    // re-review: one edit is one save, and a queued save rechecks its content
    {
      const d = await t.draft(CART_BRIEF)
      await putContent(page, d, 1, content())
      await viaApp(d)
      let puts = 0
      const count = (req) => req.method() === 'PUT' && req.url().endsWith(`/campaigns/${d}/content`) && puts++
      page.on('request', count)
      await page.locator('#sms-body').fill('One edit {{first_name}}')
      await page.locator('#email-subject').click() // blur saves
      await page.waitForTimeout(2500) // past the typing pause
      check(puts === 1, `one edit is saved once (${puts} saves)`)

      // a slow save in flight, another queued, then the SMS is cleared: the
      // queued save must not send the empty text (a 422)
      await page.route(`**/api/campaigns/${d}/content`, async (route) => {
        await new Promise((r) => setTimeout(r, 800))
        await route.continue().catch(() => {}) // the route may be removed meanwhile
      })
      let rejected = 0
      page.on('response', (r) => r.url().endsWith(`/campaigns/${d}/content`) && r.status() === 422 && rejected++)
      await page.locator('#sms-body').fill('First {{first_name}}')
      await page.locator('#email-subject').click()
      await page.locator('#sms-body').fill('Second {{first_name}}')
      await page.locator('#email-subject').click()
      await page.locator('#sms-body').fill('')
      await page.locator('#email-subject').click()
      await page.waitForTimeout(3000)
      await page.unroute(`**/api/campaigns/${d}/content`)
      page.off('request', count)
      check(rejected === 0, `a queued save never sends text cleared meanwhile (${rejected} rejected)`)
      check(await page.getByText('Not saved yet: fill in every message first.').isVisible(), 'the empty message is reported as not saved yet')
      await page.locator('#sms-body').fill('Restored {{first_name}}')
      await page.locator('#email-subject').click()
      await page.getByText('All changes saved').waitFor()
    }

    // re-review critical: an expired session while editing goes to sign-in once,
    // with no save loop and no dialogs
    {
      const d = await t.draft(CART_BRIEF)
      await putContent(page, d, 1, content())
      await viaApp(d)
      let puts = 0
      let dialogs = 0
      const count = (req) => req.method() === 'PUT' && req.url().includes('/content') && puts++
      const dismiss = (dlg) => {
        dialogs++
        dlg.dismiss()
      }
      page.on('request', count)
      page.on('dialog', dismiss)
      const cookies = await t.context.cookies()
      await t.context.clearCookies() // the session expires
      await page.locator('#sms-body').fill('Typed after the session expired')
      await page.waitForTimeout(4000)
      page.off('request', count)
      page.off('dialog', dismiss)
      check(page.url().includes('/login'), 'an expired session leads to sign-in')
      check(puts <= 2 && dialogs === 0, `no save loop and no dialogs (${puts} saves, ${dialogs} dialogs)`)
      await t.context.addCookies(cookies) // signed in again for the rest
    }

    // every language is approved on the content step (D66; the browser flow is in languages-content)
    {
      const c = await t.get(id)
      for (const lang of Object.keys(c.content)) await page.request.post(`${BASE}/api/campaigns/${id}/languages/${lang}/approve`, { data: { version: c.version } })
    }

    // review: the suggestion is preselected and labelled; approval stays explicit
    await page.goto(`${BASE}/campaigns/${id}/review`)
    await page.getByRole('heading', { name: 'When should it send?' }).waitFor()
    check(await page.getByText('Suggested from your brief:').isVisible(), 'a suggested time is labelled')
    check(await page.getByRole('radio', { name: /Schedule/ }).isChecked(), 'a usable suggestion preselects Schedule')
    const approveBtn = page.getByRole('button', { name: 'Approve and schedule…' })
    check((await approveBtn.getAttribute('aria-disabled')) === 'true', 'approval is locked until the review is confirmed')
    await approveBtn.click({ force: true })
    check(await page.getByText('Confirm that you checked the audience and the messages.').isVisible(), 'clicking early says what is missing')
    check((await page.locator('ul.blockers').getAttribute('aria-labelledby')) === 'missing-label', 'the list of what is missing is labelled (review 7)')
    check(!(await page.getByRole('alertdialog').isVisible()), 'no confirmation without the review tick')
    await page.getByLabel('I’ve checked the audience and the messages.').check()
    await approveBtn.click()
    const dlg = page.getByRole('alertdialog')
    const title = await dlg.getByRole('heading').textContent()
    check(title.startsWith('Schedule it for') && /UTC|GMT/.test(title), 'the confirmation names the time with its zone (review 6)')
    await t.shot('confirm')
    await dlg.getByRole('button', { name: 'Go back' }).click()
    await page.waitForTimeout(300)
    check((await t.get(id)).status === 'draft', '"Go back" approves nothing')

    // review fix 4: a change elsewhere before approving shows "changed elsewhere"
    const before = await t.get(id)
    await putContent(page, id, before.version, content())
    await approveBtn.click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Approve and schedule' }).click()
    await page.getByText('This draft was changed elsewhere').waitFor()
    check(await page.getByRole('button', { name: 'Reload' }).isVisible(), 'a stale approval offers Reload')
    check((await t.get(id)).status === 'draft', 'and approves nothing')

    await page.reload()
    await page.getByRole('heading', { name: 'When should it send?' }).waitFor()
    await page.getByLabel('I’ve checked the audience and the messages.').check()
    await approveBtn.click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Approve and schedule' }).click()
    await page.waitForURL(new RegExp(`/campaigns/${id}$`))
    const approved = await t.get(id)
    check(approved.status === 'approved' && approved.send_at === written.send_at, 'approved for the suggested time')

    // no time in the brief: nothing preselected
    const id2 = await t.draft(WELCOME_BRIEF, ['sms'])
    await putContent(page, id2, 1, { sms: { body: SMS } })
    await page.goto(`${BASE}/campaigns/${id2}/review`)
    await page.getByRole('heading', { name: 'When should it send?' }).waitFor()
    check(!(await page.getByRole('radio', { name: /Send now/ }).isChecked()) && !(await page.getByRole('radio', { name: /Schedule/ }).isChecked()), 'nothing is preselected without a time')
    await page.getByLabel('I’ve checked the audience and the messages.').check()
    await page.getByRole('button', { name: 'Approve and send…' }).click({ force: true })
    check(await page.getByText('Choose when to send.').isVisible(), 'approval needs a choice of when')
    check(!(await page.getByRole('alertdialog').isVisible()), 'no confirmation without that choice')

    await page.getByRole('button', { name: 'Türkçe' }).click()
    await page.getByRole('heading', { name: 'Ne zaman gönderilsin?' }).waitFor() // Turkish loads on first use (D58)
    check(await page.getByRole('heading', { name: 'Ne zaman gönderilsin?' }).isVisible(), 'the review screen in Turkish')
    await page.getByRole('button', { name: 'English' }).click()
  })
