# T23 One-step creation (D71) — plan

Branch `feat/one-step-creation` from `main`. Frontend only: the server's 409 for existing text (D66) stays the last guard against overwrites.

1. **Pure helpers (`web/src/content/languages.ts`)**, test first:
   - `chosenLanguages(raw, catalog)`: the `languages` query (comma list) → catalog codes, deduplicated, catalog order; anything else dropped.
   - `autoWrite(query, campaign, catalog)`: the languages to write automatically, or `[]`. Only when `write=ai`, the campaign is a draft, has no saved text (A or B), is not a resend, and at least one valid language was chosen.
   - Vitest: each "no" case, a valid case, junk and duplicate codes.
2. **Audience step (`CampaignView.vue`, `AudiencePanel.vue`, i18n)**: for a new draft with no text (not a resend), the language rows get checkboxes, pre-ticked with the recommendation until the marketer changes them. The footer shows "Write the messages with AI →" (disabled with nothing ticked) and "Write them myself →"; both carry `languages`, only the first adds `write=ai`. Otherwise the footer is unchanged.
3. **Content step (`ContentView.vue`)**: without saved text the chosen languages are ticked instead of the recommendation; the default is the recommended default if chosen, else the first chosen. On load, `write` is removed from the URL with `router.replace` (always), then `write(langs, false)` runs once if `autoWrite` says so. Reload, Back and Forward never see the trigger again.
4. **E2e (mock)**: interpret → pick languages → "Write the messages with AI" → drafts appear with exactly one AI request and no second click, `write` gone from the URL, reload makes no request; "Write them myself" makes no AI request. Existing flows that click "Write the messages →" use the new label.
5. **Docs**: D71, README flow.
6. Gates: focused vitest, typecheck, lint, the touched e2e files, one independent review, push, draft PR, CI. No merge without approval.
