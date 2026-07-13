import { cn } from './cn'

test('merges conditional classes and resolves Tailwind conflicts', () => {
  expect(cn('px-2', false && 'hidden', 'px-4')).toBe('px-4')
  expect(cn('text-ink-mute', undefined, 'font-semibold')).toBe('text-ink-mute font-semibold')
})

test('resolves conflicts on custom token scales (rounded-card, shadow-*)', () => {
  expect(cn('rounded', 'rounded-card')).toBe('rounded-card')
  expect(cn('shadow-md', 'shadow-elev-1')).toBe('shadow-elev-1')
  expect(cn('shadow-elev-1', 'shadow-elev-2')).toBe('shadow-elev-2')
  expect(cn('shadow-elev-1', 'shadow-pop')).toBe('shadow-pop')
})
