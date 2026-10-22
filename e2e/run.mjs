// Runs every browser test file in order; exits non-zero if any check fails.
const files = ['sign-in.mjs', 'composer.mjs', 'saving.mjs', 'languages.mjs', 'content-review.mjs']
let ok = true
for (const f of files) {
  console.log(`\n=== ${f}`)
  const mod = await import(`./${f}`)
  ok = (await mod.default()) && ok
}
console.log(ok ? '\nALL BROWSER CHECKS PASSED' : '\nSOME BROWSER CHECKS FAILED')
process.exit(ok ? 0 : 1)
