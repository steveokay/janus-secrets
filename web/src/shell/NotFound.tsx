import { Link } from 'react-router-dom'
import { Compass } from 'lucide-react'
import { EmptyState } from '../ui/EmptyState'
import { buttonClasses } from '../ui/Button'
import { useTitle } from '../lib/title'

export function NotFound() {
  useTitle('Not found')
  return (
    <EmptyState
      icon={<Compass size={22} strokeWidth={1.7} />}
      title="Page not found"
      hint="That page doesn’t exist or has moved."
      action={
        <Link to="/" className={buttonClasses('secondary')}>
          Back to home
        </Link>
      }
    />
  )
}
