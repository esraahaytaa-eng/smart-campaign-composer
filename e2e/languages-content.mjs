// Messages in each customer's language (T20: D64, D66, D67): the suggested
// languages, writing only what is asked, per-language review, and text that
// is never lost or rewritten without asking.
import { ANKARA_BRIEF, BASE, CART_BRIEF, suite } from './lib.mjs'

export default () =>
  suite('languages-content', async (t) => {
    const { page, check } = t
    const bar = page.locator('.ai-bar')
    const tab = (name) => page.locator('.tabs').getByRole('button', { name: new RegExp(`^${name}`) })
    const saved = () => page.getByText('All changes saved').waitFor()
    await t.signIn()

    const id = await t.draft(CART_BRIEF)
    // Every AI writing request for this campaign, with what it asked for.
    const writes = []
    page.on('request', (r) => r.method() === 'POST' && r.url().endsWith(`/campaigns/${id}/content`) && writes.push(r.postDataJSON()))
    const open = async () => {
      await page.goto(`${BASE}/campaigns/${id}/content`)
      await page.locator('#sms-body').waitFor()
    }

    // the suggestion is pre-ticked and can be changed; the AI writes only the ticked languages
    await open()
    const first = await t.get(id)
    check(first.recommendation?.languages.join() === 'tr,en' && first.recommendation.default === 'tr', 'the audience suggests Turkish and English, Turkish the default')
    const english = page.getByRole('checkbox', { name: /English/ })
    check((await page.getByRole('checkbox', { name: /Türkçe/ }).isChecked()) && (await english.isChecked()), 'the suggested languages are pre-ticked')
    check((await page.getByText('recommended').count()) === 2 && (await page.getByText(/% of the audience/).count()) === 2, 'labelled as recommended, with their audience shares')
    check(await page.getByText('every extra language adds AI usage').isVisible(), 'the note on AI usage is shown')
    check(await page.getByRole('checkbox', { name: /Türkçe/ }).isDisabled(), 'the default language cannot be unticked')
    await english.uncheck()
    check((await tab('English').count()) === 0, 'unticking English removes its tab')
    await page.getByRole('button', { name: 'Write with AI' }).click()
    await bar.getByText('The AI wrote the messages').waitFor({ timeout: 30000 })
    let c = await t.get(id)
    check(writes.length === 1 && writes[0].languages.join() === 'tr' && Object.keys(c.content).join() === 'tr', 'the AI wrote only Turkish')

    // approve Turkish; an edit needs review again
    check(await tab('Türkçe').getByText('Needs your review').isVisible(), 'written text needs review')
    await page.getByRole('button', { name: 'Approve Türkçe' }).click()
    await tab('Türkçe').getByText('Approved').waitFor()
    c = await t.get(id)
    check(c.languages.tr.approved_at !== null, 'Turkish is approved')
    const handTr = 'Merhaba {{first_name}}, elle yazdım!'
    await page.locator('#sms-body').fill(handTr)
    await page.locator('#email-subject').click()
    await saved()
    await tab('Türkçe').getByText('Needs your review').waitFor()
    c = await t.get(id)
    check(c.languages.tr.approved_at === null && c.content.tr.sms.body === handTr, 'an edit needs review again')
    await page.getByRole('button', { name: 'Approve Türkçe' }).click()
    await tab('Türkçe').getByText('Approved').waitFor()

    // add English after hand-editing Turkish: one request for English only
    await english.check()
    check(await tab('English').getByText('Not written yet').isVisible(), 'the added language has an empty tab')
    await page.getByRole('button', { name: 'Write with AI' }).click()
    await bar.getByText('The AI wrote the messages').waitFor({ timeout: 30000 })
    c = await t.get(id)
    check(writes.length === 2 && writes[1].languages.join() === 'en' && writes[1].replace === false, 'one request, for English only')
    check(c.content.tr.sms.body === handTr && c.languages.tr.approved_at !== null, 'Turkish keeps its hand edit and its approval')
    check(!!c.content.en?.sms?.body && c.languages.en.approved_at === null, 'English is written and needs review')
    check((await page.locator('#sms-body').inputValue()) === c.content.en.sms.body, 'the English tab shows its text')
    await page.getByRole('button', { name: 'Approve English' }).click()
    await tab('English').getByText('Approved').waitFor()

    // changing every setting calls the AI zero times
    {
      const before = writes.length
      await page.getByRole('button', { name: 'Make default' }).click()
      await saved()
      check((await t.get(id)).default_language === 'en', 'English made the default')
      await tab('Türkçe').click()
      await page.getByRole('button', { name: 'Make default' }).click()
      await saved()
      await page.getByRole('button', { name: /Long name/ }).click()
      await tab('English').click()
      await page.goto(`${BASE}/campaigns/${id}`)
      await page.getByText('Estimated audience').waitFor()
      await page.getByRole('button', { name: 'SMS', exact: true }).click()
      await saved()
      check(await page.getByText('The email text is kept, not sent.').isVisible(), 'removing email says its text is kept')
      await open()
      check(await page.getByText('The email text is kept but not sent.').isVisible(), 'the content step shows it as kept, not sent')
      await page.goto(`${BASE}/campaigns/${id}`)
      await page.getByText('Estimated audience').waitFor()
      await page.getByRole('button', { name: 'Both', exact: true }).click()
      await saved()
      c = await t.get(id)
      check(c.content.tr.email?.subject && c.content.en.email?.subject && c.languages.tr.approved_at === null && c.languages.en.approved_at === null, 'choosing email again brings its text back, to be reviewed again')
      check(writes.length === before, `no setting calls the AI (${writes.length - before} calls)`)
    }

    // a new brief: every language needs review, the text is unchanged, and it survives a reload
    {
      const before = await t.get(id)
      const r = await page.request.put(`${BASE}/api/campaigns/${id}/request`, { data: { version: before.version, request: ANKARA_BRIEF } })
      check(r.ok(), 'test setup: the brief is changed')
      await open()
      for (const l of ['Türkçe', 'English']) check(await tab(l).getByText('May be outdated').isVisible(), `${l} may be outdated`)
      check(await page.getByText('The brief changed after this text was written.').isVisible(), 'the tab says why')
      await page.reload()
      await page.locator('#sms-body').waitFor()
      check(await tab('English').getByText('May be outdated').isVisible(), 'the warning survives a reload')
      c = await t.get(id)
      check(JSON.stringify(c.content) === JSON.stringify(before.content), 'the text is unchanged')
      // approved as it is: no new text, no AI call (clarification 2)
      const calls = writes.length
      await tab('Türkçe').click()
      await page.getByRole('button', { name: 'Approve Türkçe' }).click()
      await tab('Türkçe').getByText('Approved').waitFor()
      check(writes.length === calls && (await t.get(id)).content.tr.sms.body === handTr, 'approving outdated text as it is calls no AI and keeps it')
    }

    // cancelling an overwrite changes nothing
    {
      const before = await t.get(id)
      await page.getByRole('button', { name: 'Rewrite Türkçe with AI' }).click()
      const dlg = page.getByRole('alertdialog')
      check((await dlg.textContent()).includes('Rewrite the messages in Türkçe?'), 'rewriting a written language asks first')
      await dlg.getByRole('button', { name: 'Cancel' }).click()
      await page.waitForTimeout(300)
      const after = await t.get(id)
      check(after.version === before.version && JSON.stringify(after.content) === JSON.stringify(before.content) && writes.length === 2, 'cancelling changes nothing')
    }
    await t.shot('languages-content')
  })
