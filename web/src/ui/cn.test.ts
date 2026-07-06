import { cn } from './cn'

test('merges conditional classes and resolves Tailwind conflicts', () => {
  expect(cn('px-2', false && 'hidden', 'px-4')).toBe('px-4')
  expect(cn('text-muted', undefined, 'font-semibold')).toBe('text-muted font-semibold')
})

test('resolves conflicts on custom token scales (rounded-card, shadow-*)', () => {
  expect(cn('rounded', 'rounded-card')).toBe('rounded-card')
  expect(cn('shadow-md', 'shadow-card')).toBe('shadow-card')
  expect(cn('shadow-card', 'shadow-pop')).toBe('shadow-pop')
})
