import { useTitle } from '../lib/title'
import { useProjects } from '../secrets/nav'
import { HomeHeader } from './HomeHeader'
import { StatCards } from './StatCards'
import { HomeProjects } from './HomeProjects'
import { ActivityFeed } from './ActivityFeed'

export function HomePage() {
  useTitle('Home')
  // Fetched once here; count feeds the header, the list feeds the cards.
  const projects = useProjects()
  return (
    <div className="mx-auto max-w-[1100px]">
      <h1 className="sr-only">Home</h1>
      <HomeHeader projectCount={projects.data?.length ?? 0} />
      <StatCards projects={projects.data ?? []} />
      <HomeProjects projects={projects} />
      <ActivityFeed />
    </div>
  )
}
