# T22 Audience rules (D70) — plan

Branch `feat/audience-rules` from `main`. Rules stay a flat AND over a fixed catalog; values reach SQL only as arguments.

1. **Backend (`internal/audience`)**, test first:
   - `last_months` (1–12) in `window`: exactly one of `last_days`, `last_months`, `from`; calendar months back in the campaign zone, day clamped to the month's end.
   - Ops `gt`/`lt` on `order_count`, `total_spend`; `gt` on `cart_value`.
   - `spend_in_window` (`{"amount", window}`), `orders_in_window` (`{"count", window}`); ops gte/lte/gt/lt; value types `amount_window`, `count_window`.
   - `signed_up before` (no `to`).
   - `FixedWindowStarts` covers the new windowed fields; `Limits` gains `max_months`.
   - Tests: validation accept/reject cases, catalog/Validate agreement, SQL on the fixture (strict vs inclusive at the exact amount, window edges, month clamping, signed-up-before, the full realistic brief).
2. **AI (`internal/llm`)**: prompt lines and answer schema for the new fields, ops and `last_months`; mock understands the realistic brief (EN, TR). Tests: schema lists every catalog field/op; mock rules validate.
3. **UI (`web/src/campaign/rules.ts`, `RuleEditor.vue`, i18n)**: chips and titles for every new pair, a "last N months" time mode, amount/count + window form. Vitest for labels (EN, TR), form round-trips and problems.
4. **Docs**: README rule list and mock briefs.
5. Gates: focused Go and web tests, lint, one independent review, one controlled real-AI call for the realistic brief, push, draft PR, CI.
