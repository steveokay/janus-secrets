import { cn } from './cn'

test('merges conditional classes and resolves Tailwind conflicts', () => {
  expect(cn('px-2', false && 'hidden', 'px-4')).toBe('px-4')
  expect(cn('text-muted', undefined, 'font-semibold')).toBe('text-muted font-semibold')
})
