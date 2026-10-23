// README screenshots and a browser smoke test, against the demo library (scripts/demo-library: Creative
// Commons albums in a throwaway Navidrome) and a Pocket Music started with DEMO_DATA=true:
//
//   docker run -d -p 127.0.0.1:4535:4533 -v ~/.cache/pm-demo:/data -v <library>:/music:ro \
//     -e ND_DEVAUTOCREATEADMINPASSWORD=devpass123 deluan/navidrome
//   DATA_DIR=/tmp/pm DEMO_DATA=true ADMIN_EMAIL=ana@example.com ADMIN_PASSWORD=demo-password ADMIN_NAME="Ana Petrović" \
//     NAVIDROME_URL=http://127.0.0.1:4535 NAVIDROME_USER=admin NAVIDROME_PASSWORD=devpass123 PORT=8298 ./pocket-music
//   BASE=http://127.0.0.1:8298 node scripts/screenshots.mjs                  # Playwright's own Chromium
//   CDP=http://127.0.0.1:9222 BASE=… node scripts/screenshots.mjs            # or any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                   # checks only
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:8298'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/409|Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'ana@example.com', password: 'demo-password' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
const settle = page => page.waitForTimeout(1600)
async function go(page, path) { await page.goto(base + path, { waitUntil: 'networkidle' }); await page.evaluate(() => document.fonts.ready); await settle(page) }
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/')
  assert.ok(await page.locator('.card-tile.mix').count() >= 3, 'daily mixes on Home')
  assert.ok(await page.locator('.shelf').count() >= 3, 'shelves on Home')
  await shot(page, 'desktop-home')

  // an album, playing
  await go(page, '/albums')
  const first = page.locator('.grid-tiles .card-tile').filter({ hasText: 'Piano Paintings' }).first()
  await first.click()
  await settle(page)
  await page.click('.play-btn')
  await page.waitForTimeout(3500)
  const playing = await page.evaluate(() => window.pocketPlayer.state.playing && window.pocketPlayer.position > 0.5)
  assert.ok(playing, 'an album plays')
  await page.mouse.move(700, 600)
  await shot(page, 'desktop-album')

  await page.keyboard.press('n')
  await page.waitForTimeout(1800)
  assert.ok(await page.locator('.np').isVisible(), 'Now Playing opens')
  await shot(page, 'desktop-nowplaying')
  await page.keyboard.press('Escape')

  await go(page, '/stats?period=month')
  assert.ok(await page.locator('.chart .bar').count() > 5, 'stats chart')
  await shot(page, 'desktop-stats')

  await go(page, '/')
  await page.keyboard.press('x')
  await page.keyboard.type('calm piano and soundtracks for a rainy sunday')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.mix-song', { timeout: 15000 })
  await page.waitForTimeout(800)
  await shot(page, 'desktop-mix')
  await page.keyboard.press('Escape')

  await go(page, '/smart/new')
  await page.locator('.chips .chip').filter({ hasText: 'Heavy rotation' }).click()
  await page.waitForTimeout(1500)
  await shot(page, 'desktop-smart')

  await go(page, '/artists')
  await page.locator('.card-tile').filter({ hasText: 'Chris Zabriskie' }).first().click()
  await settle(page)
  await shot(page, 'desktop-artist')
  await ctx.close()
}
{
  const { ctx, page } = await session({ dark: true })
  await go(page, '/')
  await page.evaluate(() => window.pocketPlayer.pause())
  await go(page, '/mixes/daily-1')
  await shot(page, 'desktop-mix-dark')
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/')
  await shot(page, 'phone-home')
  await go(page, '/albums')
  await page.locator('.grid-tiles .card-tile').filter({ hasText: 'Forgotten Stars' }).first().click()
  await settle(page)
  await page.click('.play-btn')
  await page.waitForTimeout(3000)
  await page.locator('.mini-bar').click({ position: { x: 120, y: 20 } })
  await page.waitForTimeout(1500)
  await shot(page, 'phone-nowplaying')
  await ctx.close()
}

await browser.close()
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log(shots ? `screenshots written to ${dir}` : 'checks passed')
