// Real-browser smoke: boots the served bundle in headless Edge/Chrome with
// REAL-shape /v1 fixtures and fails on page errors or an empty root.
// Codifies the rule from the 2026-07-06 blank-page incident: unit tests with
// invented mock shapes cannot catch authed-shell crashes.
import { existsSync } from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_URL ?? 'http://127.0.0.1:8210'
const exe = [
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
].find((p) => existsSync(p))
if (!exe) { console.error('smoke: no browser binary found'); process.exit(2) }

// Shapes mirror internal/api handlers — update alongside the Go code.
const fixtures = {
  '/v1/sys/seal-status': { initialized: true, sealed: false, type: 'shamir', threshold: 1, shares: 1 },
  '/v1/auth/me': { kind: 'user', id: 'u1', name: 'smoke@janus.dev' },
  '/v1/projects': { projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] },
  '/v1/projects/p1/environments': { environments: [] },
}

const browser = await puppeteer.launch({ executablePath: exe, headless: 'new' })
const page = await browser.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(String(e)))
await page.setRequestInterception(true)
page.on('request', (req) => {
  const path = new URL(req.url()).pathname
  if (path in fixtures) {
    return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(fixtures[path]) })
  }
  if (path.startsWith('/v1/')) {
    return req.respond({ status: 404, contentType: 'application/json', body: '{"error":{"code":"not_found","message":"smoke fixture missing"}}' })
  }
  return req.continue()
})
await page.goto(BASE + '/', { waitUntil: 'networkidle0', timeout: 20000 })
await new Promise((r) => setTimeout(r, 800))
const html = await page.evaluate(() => document.getElementById('root')?.innerHTML ?? '')

// Dark pass: seed the stored theme, reload, and confirm html.dark applied +
// the shell still renders. Request interception + fixtures persist across reload.
await page.evaluateOnNewDocument(() => localStorage.setItem('janus.theme', 'dark'))
await page.reload({ waitUntil: 'networkidle0', timeout: 20000 })
await new Promise((r) => setTimeout(r, 800))
const darkOn = await page.evaluate(() => document.documentElement.classList.contains('dark'))
const darkHtml = await page.evaluate(() => document.getElementById('root')?.innerHTML ?? '')
const darkBg = await page.evaluate(() =>
  getComputedStyle(document.documentElement).backgroundColor,
)

await browser.close()

// Gate the actual rendered background, not just the class: if theme.css failed
// to bundle/load, html.dark would still be present (JS-added) while the page
// stayed light. #0B0B10 (dark page) sums to 38; #F6F6FA (light) sums to 742.
const darkBgNums = (darkBg.match(/\d+/g) || []).map(Number)
const darkBgIsDark = darkBgNums.length >= 3 && darkBgNums[0] + darkBgNums[1] + darkBgNums[2] < 120
const lightOk = errors.length === 0 && html.length >= 500 && html.includes('Janus')
const darkOk = darkOn && darkBgIsDark && darkHtml.length >= 500 && darkHtml.includes('Janus')
if (!lightOk || !darkOk) {
  console.error('SMOKE FAILED', JSON.stringify(
    { errors, rootLength: html.length, darkOn, darkRootLength: darkHtml.length, darkBg }, null, 2))
  process.exit(1)
}
console.log(`smoke ok — light (${html.length} chars) + dark (${darkHtml.length} chars, bg ${darkBg})`)
