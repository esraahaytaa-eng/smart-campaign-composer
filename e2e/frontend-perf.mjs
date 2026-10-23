// Frontend measurements for make perf (T19 step 7, P7): what a first visit
// downloads and runs, and how quickly the page answers clicks and typing, on
// a desktop and on a phone profile (Lighthouse's mobile defaults: slow 4G and
// a 4x slower CPU). Every load is cold (a new browser context, empty cache).
// Prints medians of RUNS runs; measures only, never fails on a number.
import { chromium } from 'playwright'
import { BASE } from './lib.mjs'

const EMAIL = process.env.DEMO_EMAIL ?? 'marketer@marketer.com'
const PASSWORD = process.env.DEMO_PASSWORD ?? 'Demo1234!'
const RUNS = Number(process.env.RUNS ?? 3)

const PROFILES = {
  desktop: { viewport: { width: 1440, height: 900 } },
  // DevTools' "Slow 4G": 562.5 ms round trip, 1.4 Mbps down, 675 kbps up.
  phone: {
    viewport: { width: 375, height: 812 },
    isMobile: true,
    hasTouch: true,
    deviceScaleFactor: 3,
    cpu: 4,
    network: { offline: false, latency: 562.5, downloadThroughput: (1.4 * 1024 * 1024) / 8, uploadThroughput: (675 * 1024) / 8 },
  },
}

// Recorded from the very start of each page: long main-thread tasks,
// interactions (Event Timing) and the largest paint.
function observe() {
  window.__perf = { longTasks: [], events: [], lcp: 0 }
  new PerformanceObserver((l) => window.__perf.longTasks.push(...l.getEntries().map((e) => e.duration))).observe({ type: 'longtask', buffered: true })
  new PerformanceObserver((l) => window.__perf.events.push(...l.getEntries().filter((e) => e.interactionId).map((e) => ({ name: e.name, id: e.interactionId, duration: e.duration }))))
    .observe({ type: 'event', durationThreshold: 16, buffered: true })
  new PerformanceObserver((l) => (window.__perf.lcp = l.getEntries().at(-1).startTime)).observe({ type: 'largest-contentful-paint', buffered: true })
}

async function open(browser, profile, storageState) {
  const { cpu, network, ...options } = PROFILES[profile]
  const context = await browser.newContext({ ...options, locale: 'en-GB', storageState })
  await context.addInitScript(observe)
  const page = await context.newPage()
  const cdp = await context.newCDPSession(page)
  await cdp.send('Performance.enable')
  await cdp.send('Network.enable')
  if (cpu) await cdp.send('Emulation.setCPUThrottlingRate', { rate: cpu })
  if (network) await cdp.send('Network.emulateNetworkConditions', network)
  const scripts = new Map() // requestId → { url, bytes }
  cdp.on('Network.responseReceived', (e) => e.type === 'Script' && scripts.set(e.requestId, { url: e.response.url, encoding: e.response.headers['content-encoding'] ?? 'none' }))
  cdp.on('Network.loadingFinished', (e) => scripts.has(e.requestId) && (scripts.get(e.requestId).bytes = e.encodedDataLength))
  return { context, page, cdp, scripts }
}

// A cold load of path; ready is the element that means "usable".
async function coldLoad(browser, profile, path, ready, storageState) {
  const { context, page, cdp, scripts } = await open(browser, profile, storageState)
  await page.goto(BASE + path, { waitUntil: 'load' })
  await ready(page).waitFor({ timeout: 60000 })
  const usable = await page.evaluate(() => performance.now())
  await page.waitForTimeout(300)
  const timing = await page.evaluate(() => {
    const nav = performance.getEntriesByType('navigation')[0]
    const fcp = performance.getEntriesByName('first-contentful-paint')[0]?.startTime ?? 0
    return { ttfb: nav.responseStart, fcp, lcp: window.__perf.lcp, dcl: nav.domContentLoadedEventEnd, load: nav.loadEventEnd, longTasks: window.__perf.longTasks }
  })
  const metrics = Object.fromEntries((await cdp.send('Performance.getMetrics')).metrics.map((m) => [m.name, m.value]))
  const js = [...scripts.values()]
  await context.close()
  return {
    ...timing,
    usable,
    scriptMs: metrics.ScriptDuration * 1000, // main thread running JavaScript (CPU-throttled time on the phone)
    taskMs: metrics.TaskDuration * 1000,
    layoutMs: (metrics.LayoutDuration + metrics.RecalcStyleDuration) * 1000, // style and layout
    tbt: timing.longTasks.reduce((s, d) => s + Math.max(0, d - 50), 0),
    jsFiles: js.length,
    jsKB: js.reduce((s, f) => s + (f.bytes ?? 0), 0) / 1000,
    encodings: [...new Set(js.map((f) => f.encoding))].join(','),
  }
}

async function signedInState(browser) {
  const context = await browser.newContext()
  const page = await context.newPage()
  await page.goto(BASE + '/login')
  await page.getByLabel('Work email').fill(EMAIL)
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.waitForURL((u) => !u.pathname.startsWith('/login'))
  const state = await context.storageState()
  await context.close()
  return state
}

// Interactions on a warm page: each one's slowest event (what INP reports)
// and the time until its result is on screen (including the server).
async function interactions(browser, profile, storageState) {
  const { context, page } = await open(browser, profile, storageState)
  const out = {}
  const measure = async (name, act, done) => {
    const before = await page.evaluate(() => performance.now())
    await act()
    await done()
    const after = await page.evaluate(() => performance.now())
    await page.waitForTimeout(200) // let Event Timing report the interaction
    const slowest = await page.evaluate(() => {
      const durations = window.__perf.events.map((e) => e.duration)
      window.__perf.events = []
      return Math.max(0, ...durations)
    })
    out[name] = { inp: slowest, result: after - before }
  }
  await page.goto(BASE + '/campaigns', { waitUntil: 'load' })
  await page.getByText(/^Showing /).waitFor({ timeout: 60000 })
  await page.evaluate(() => (window.__perf.events = []))
  const showing = () => page.getByText(/^Showing /).innerText()
  let shown = await showing()
  await measure('filter: Draft', () => page.getByRole('button', { name: /^Draft\b/ }).click(), async () => {
    await page.waitForURL((u) => u.searchParams.get('status') === 'draft')
    await page.waitForFunction((s) => document.body.innerText.includes('Showing') && !document.body.innerText.includes(s), shown)
  })
  shown = await showing()
  await measure('search: type "istanbul"', () => page.getByRole('searchbox', { name: 'Search campaigns' }).pressSequentially('istanbul', { delay: 80 }), async () => {
    await page.waitForURL((u) => u.searchParams.get('q') === 'istanbul')
    await page.waitForFunction((s) => document.body.innerText.includes('Showing') && !document.body.innerText.includes(s), shown)
  })
  await measure('language: Türkçe, first time', () => page.getByRole('button', { name: 'Türkçe' }).click(), () => page.getByRole('heading', { name: 'Kampanyalar' }).waitFor())
  await measure('language: back to English', () => page.getByRole('button', { name: 'English' }).click(), () => page.getByRole('heading', { name: 'Campaigns' }).waitFor())
  await measure('open a campaign', () => page.locator('[role=row] a.title').first().click(), () => page.getByText(/^(Draft|Approved|Completed|Cancelled|Sending)$/).first().waitFor())
  await context.close()
  return out
}

const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)]
const ms = (v) => `${Math.round(v)}`

const browser = await chromium.launch()
const state = await signedInState(browser)
for (const profile of Object.keys(PROFILES)) {
  console.log(`\n=== ${profile}${PROFILES[profile].cpu ? ` (CPU ${PROFILES[profile].cpu}x slower, slow 4G)` : ''}: medians of ${RUNS} cold loads`)
  console.log('page'.padEnd(16) + ['TTFB', 'FCP', 'LCP', 'usable', 'load', 'JS run', 'layout', 'tasks', 'TBT', 'JS kB', 'files'].map((h) => h.padStart(8)).join('') + '  encoding')
  for (const [name, path, ready, auth] of [
    ['sign-in', '/login', (p) => p.getByLabel('Work email'), undefined],
    ['dashboard', '/', (p) => p.getByRole('heading', { name: 'Your campaigns' }), state],
    ['campaign list', '/campaigns', (p) => p.getByText(/^Showing /), state],
  ]) {
    const runs = []
    for (let i = 0; i < RUNS; i++) runs.push(await coldLoad(browser, profile, path, ready, auth))
    const m = (k) => median(runs.map((r) => r[k]))
    console.log(name.padEnd(16) + [m('ttfb'), m('fcp'), m('lcp'), m('usable'), m('load'), m('scriptMs'), m('layoutMs'), m('taskMs'), m('tbt')].map((v) => ms(v).padStart(8)).join('') +
      m('jsKB').toFixed(1).padStart(8) + String(m('jsFiles')).padStart(8) + '  ' + runs[0].encodings)
  }
  const inter = []
  for (let i = 0; i < RUNS; i++) inter.push(await interactions(browser, profile, state))
  console.log('\n' + 'interaction'.padEnd(32) + 'slowest event (INP)'.padStart(22) + 'result on screen'.padStart(20))
  for (const name of Object.keys(inter[0])) {
    console.log(name.padEnd(32) + `${ms(median(inter.map((r) => r[name].inp)))} ms`.padStart(22) + `${ms(median(inter.map((r) => r[name].result)))} ms`.padStart(20))
  }
}
await browser.close()
console.log('\nTimes in ms from the start of navigation. "usable": the page\'s main element is on screen. "JS run": main-thread time spent running JavaScript; "layout": style and layout; "tasks": all main-thread work.')
