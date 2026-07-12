import { useTitle } from '../lib/title'

export function HomePage() {
  useTitle('Home')
  return (
    <div className="mx-auto max-w-[1100px]">
      <h1 className="sr-only">Home</h1>
      {/* HomeHeader / StatCards / HomeProjects / ActivityFeed compose here in later tasks */}
    </div>
  )
}
