import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { PaletteProvider, usePalette } from './PaletteProvider'

function Opener() {
  const { open } = usePalette()
  return <button onClick={open}>open-palette</button>
}

function shell() {
  const qc = new QueryClient()
  qc.setQueryData(['projects'], [{ id: 'p1', slug: 'gw', name: 'api-gateway' }])
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <PaletteProvider><Opener /></PaletteProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

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
