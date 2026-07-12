import { useTitle } from '../lib/title'
import { useProjects } from '../secrets/nav'
import { HomeHeader } from './HomeHeader'
import { HomeProjects } from './HomeProjects'

export function HomePage() {
  useTitle('Home')
  // Fetched once here; count feeds the header, the list feeds the cards.
  const projects = useProjects()
  return (
    <div className="mx-auto max-w-[1100px]">
      <h1 className="sr-only">Home</h1>
      <HomeHeader projectCount={projects.data?.length ?? 0} />
      <HomeProjects projects={projects} />
      {/* StatCards / ActivityFeed compose here in later tasks */}
    </div>
  )
}
