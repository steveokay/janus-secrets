// Read-only responsive screenshot harness.
//
// NOT part of the test suite — a local tool for eyeballing the Atrium layout at
// phone/tablet widths in both themes. It logs in and navigates; it never
// creates, edits or deletes anything, so it is safe to point at a live dev
// stack (unlike smoke.spec.ts, which runs the init ceremony and needs a fresh
// server).
//
// Credentials are parsed out of the git-excluded access.md at runtime so they
// are never passed on a command line or printed.
//
//   node tests/e2e/shots.mjs [outDir]

import { chromium } from '@playwright/test'
import { readFileSync, mkdirSync } from 'node:fs'
import { join, resolve } from 'node:path'

const BASE = process.env.JANUS_E2E_BASE_URL ?? 'http://localhost:8210'
const OUT = resolve(process.argv[2] ?? './shots')
const ACCESS = resolve('../access.md')

function creds() {
  const md = readFileSync(ACCESS, 'utf8')
  const grab = (label) => {
    const m = md.match(new RegExp(`\\|\\s*${label}\\s*\\|\\s*\`([^\`]+)\``, 'i'))
    if (!m) throw new Error(`could not find "${label}" in ${ACCESS}`)
    return m[1]
  }
  return { email: grab('Email'), password: grab('Password') }
}

const VIEWPORTS = [
  { name: 'phone', width: 390, height: 844 },   // iPhone 14/15
  { name: 'tablet', width: 768, height: 1024 }, // iPad portrait
  { name: 'laptop', width: 1280, height: 800 },
]

const SCREENS = [
  { name: 'overview', path: '/' },
  { name: 'audit', path: '/audit' },
  { name: 'approvals', path: '/approvals' },
  { name: 'projects', path: '/projects' },
  { name: 'tokens', path: '/tokens' },
]

const results = []

const browser = await chromium.launch()
try {
  for (const vp of VIEWPORTS) {
    const ctx = await browser.newContext({
      viewport: { width: vp.width, height: vp.height },
      deviceScaleFactor: 2,
      isMobile: vp.name !== 'laptop',
      hasTouch: vp.name !== 'laptop',
    })
    const page = await ctx.newPage()

    await page.goto(BASE)
    // Log in if the gate is showing (a fresh context always is).
    const emailBox = page.locator('#email')
    if (await emailBox.isVisible().catch(() => false)) {
      const { email, password } = creds()
      await emailBox.fill(email)
      await page.locator('#pw').fill(password)
      // The submit button is "Sign the register" (Atrium voice) and changes
      // text while busy / when TOTP is required — target the role, not the copy.
      await page.locator('form button[type="submit"]').first().click()
    }
    // Wait on the folio bar, not a nav link: below the breakpoint the nav lives
    // in a closed drawer and is legitimately `visibility: hidden`.
    await page.locator('.folio-bar').first().waitFor({ timeout: 20_000 })

    for (const theme of ['daylight', 'nightwatch']) {
      // Must match src/lib/theme.svelte.ts exactly: the key is `atrium-theme`,
      // and daylight is expressed by REMOVING data-theme, not setting it. The
      // store re-applies from localStorage on every load, so setting only the
      // attribute silently reverts on the next navigation.
      await page.evaluate((t) => {
        localStorage.setItem('atrium-theme', t)
        if (t === 'nightwatch') document.documentElement.setAttribute('data-theme', 'nightwatch')
        else document.documentElement.removeAttribute('data-theme')
      }, theme)

      for (const s of SCREENS) {
        await page.goto(BASE + s.path)
        await page.waitForTimeout(700) // let data land + animations settle
        mkdirSync(join(OUT, vp.name), { recursive: true })
        const file = join(OUT, vp.name, `${s.name}-${theme}.png`)
        await page.screenshot({ path: file })

        // Horizontal overflow is the headline mobile failure — measure it.
        //
        // Checking only the document is NOT enough: the shell's .desk is
        // `overflow: hidden`, so oversized content is silently CLIPPED rather
        // than pushing the page wide, and the document measures clean while the
        // UI is unusable. So also measure the scrolling content area itself.
        const m = await page.evaluate(() => {
          const page_ = document.querySelector('.page')
          return {
          scrollW: document.documentElement.scrollWidth,
          clientW: document.documentElement.clientWidth,
          pageScrollW: page_ ? page_.scrollWidth : 0,
          pageClientW: page_ ? page_.clientWidth : 0,
          // widest element actually sticking out of the viewport
          worst: (() => {
            let worst = { tag: '', cls: '', right: 0 }
            const vw = document.documentElement.clientWidth
            for (const el of document.querySelectorAll('*')) {
              const r = el.getBoundingClientRect()
              if (r.width > 0 && r.right > vw + 1 && r.right > worst.right) {
                worst = {
                  tag: el.tagName.toLowerCase(),
                  cls: (el.className?.baseVal ?? el.className ?? '').toString().slice(0, 60),
                  right: Math.round(r.right),
                }
              }
            }
            return worst
          })(),
        }})
        results.push({ vp: vp.name, theme, screen: s.name, ...m })
      }

      // The drawer only exists below the breakpoint — capture it open too.
      const toggle = page.locator('.nav-toggle')
      if (await toggle.isVisible().catch(() => false)) {
        await page.goto(BASE + '/')
        await page.waitForTimeout(400)
        await toggle.click()
        await page.waitForTimeout(450) // slide
        await page.screenshot({ path: join(OUT, vp.name, `drawer-open-${theme}.png`) })
      }
    }
    await ctx.close()
  }
} finally {
  await browser.close()
}

console.log(`\nshots → ${OUT}\n`)
const bad = results.filter(
  (r) => r.scrollW > r.clientW + 1 || r.pageScrollW > r.pageClientW + 1,
)
if (bad.length === 0) {
  console.log('no horizontal overflow at any viewport ✓')
} else {
  console.log(`HORIZONTAL OVERFLOW in ${bad.length}/${results.length}:`)
  for (const b of bad) {
    console.log(
      `  ${b.vp.padEnd(7)} ${b.theme.padEnd(11)} ${b.screen.padEnd(10)} ` +
        `doc ${b.scrollW}/${b.clientW}  page ${b.pageScrollW}/${b.pageClientW}  ` +
        `worst: <${b.worst.tag} class="${b.worst.cls}"> right=${b.worst.right}`,
    )
  }
}
