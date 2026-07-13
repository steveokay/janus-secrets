import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom'
import { afterEach, expect, test, vi } from 'vitest'
import { ThemeProvider } from '../theme/ThemeProvider'
import { PaletteProvider, usePalette } from './PaletteProvider'

function Opener() {
  const { open } = usePalette()
  return <button onClick={open}>open-palette</button>
}

function LocationProbe() {
  const loc = useLocation()
  return <div data-testid="loc">{loc.pathname + loc.search}</div>
}

function shell(route = '/') {
  const qc = new QueryClient()
  qc.setQueryData(['projects'], [{ id: 'p1', slug: 'gw', name: 'api-gateway' }])
  return render(
    <QueryClientProvider client={qc}>
      <ThemeProvider>
        <MemoryRouter initialEntries={[route]}>
          <PaletteProvider>
            <Opener />
            <LocationProbe />
            <Routes>
              <Route path="*" element={null} />
            </Routes>
          </PaletteProvider>
        </MemoryRouter>
      </ThemeProvider>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  vi.restoreAllMocks()
  document.documentElement.classList.remove('dark')
})

test('Cmd/Ctrl+K opens the palette', async () => {
  shell()
  expect(screen.queryByRole('combobox', { name: /search/i })).not.toBeInTheDocument()
  await userEvent.keyboard('{Control>}k{/Control}')
  expect(await screen.findByRole('combobox', { name: /search/i })).toBeInTheDocument()
})

test('usePalette().open() opens it and shows a project', async () => {
  shell()
  await userEvent.click(screen.getByText('open-palette'))
  expect(await screen.findByText('api-gateway')).toBeInTheDocument()
})

test('selecting a nav command navigates via `to`', async () => {
  shell()
  await userEvent.click(screen.getByText('open-palette'))
  await userEvent.click(await screen.findByRole('option', { name: /go to projects/i }))
  expect(screen.getByTestId('loc')).toHaveTextContent('/projects')
})

test('selecting "New project" navigates to /projects?new=1', async () => {
  shell()
  await userEvent.click(screen.getByText('open-palette'))
  await userEvent.click(await screen.findByRole('option', { name: /^new project$/i }))
  expect(screen.getByTestId('loc')).toHaveTextContent('/projects?new=1')
})

test('selecting "Toggle theme" flips the dark class', async () => {
  document.documentElement.classList.remove('dark') // start light
  shell()
  await userEvent.click(screen.getByText('open-palette'))
  await userEvent.click(await screen.findByRole('option', { name: /toggle theme/i }))
  expect(document.documentElement.classList.contains('dark')).toBe(true)
})

test('selecting "Export audit (CSV)" clicks a download anchor for the export URL', async () => {
  const clickSpy = vi
    .spyOn(HTMLAnchorElement.prototype, 'click')
    .mockImplementation(() => {})
  const realCreate = document.createElement.bind(document)
  let anchor: HTMLAnchorElement | undefined
  vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
    const el = realCreate(tag)
    if (tag === 'a') anchor = el as HTMLAnchorElement
    return el
  })

  shell()
  await userEvent.click(screen.getByText('open-palette'))
  await userEvent.click(await screen.findByRole('option', { name: /export audit/i }))

  expect(clickSpy).toHaveBeenCalledTimes(1)
  expect(anchor).toBeDefined()
  expect(anchor!.getAttribute('href')).toContain('/v1/audit/export')
  expect(anchor!.getAttribute('href')).toContain('format=csv')
  expect(anchor!.getAttribute('download')).toBe('audit.csv')
  // Cleaned up after click.
  expect(document.body.contains(anchor!)).toBe(false)
})
