import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { ThemeProvider, useTheme } from './ThemeProvider'

function Probe() {
  const { theme, resolved, setTheme } = useTheme()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <span data-testid="resolved">{resolved}</span>
      <button onClick={() => setTheme('dark')}>dark</button>
      <button onClick={() => setTheme('light')}>light</button>
      <button onClick={() => setTheme('system')}>system</button>
    </div>
  )
}

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove('dark')
  window.matchMedia = vi.fn().mockImplementation((q: string) => ({
    matches: false, media: q, onchange: null,
    addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia
})

test('defaults to system and applies light when OS prefers light', () => {
  render(<ThemeProvider><Probe /></ThemeProvider>)
  expect(screen.getByTestId('theme').textContent).toBe('system')
  expect(screen.getByTestId('resolved').textContent).toBe('light')
  expect(document.documentElement.classList.contains('dark')).toBe(false)
})

test('setTheme("dark") adds the dark class and persists', async () => {
  render(<ThemeProvider><Probe /></ThemeProvider>)
  await userEvent.click(screen.getByText('dark'))
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  expect(localStorage.getItem('janus.theme')).toBe('dark')
  expect(screen.getByTestId('resolved').textContent).toBe('dark')
})

test('setTheme("light") removes the dark class and persists', async () => {
  localStorage.setItem('janus.theme', 'dark')
  render(<ThemeProvider><Probe /></ThemeProvider>)
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  await userEvent.click(screen.getByText('light'))
  expect(document.documentElement.classList.contains('dark')).toBe(false)
  expect(localStorage.getItem('janus.theme')).toBe('light')
})

test('system mode follows matchMedia = dark', () => {
  window.matchMedia = vi.fn().mockImplementation((q: string) => ({
    matches: true, media: q, onchange: null,
    addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia
  render(<ThemeProvider><Probe /></ThemeProvider>)
  expect(screen.getByTestId('resolved').textContent).toBe('dark')
  expect(document.documentElement.classList.contains('dark')).toBe(true)
})
