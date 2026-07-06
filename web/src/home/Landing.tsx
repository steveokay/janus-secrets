import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Plus } from 'lucide-react'
import { Brand } from '../ui/Brand'
import { useProjects } from '../secrets/nav'
import { CreateProjectForm } from '../structure/CreateForms'

export function Landing() {
  const projects = useProjects()
  const navigate = useNavigate()
  const [creating, setCreating] = useState(false)

  const createDialog = creating && (
    <CreateProjectForm
      onCreated={(p) => { setCreating(false); navigate('/projects/' + p.id) }}
      onClose={() => setCreating(false)}
    />
  )

  if (projects.isLoading) {
    return (
      <div aria-hidden className="mx-auto mt-20 flex max-w-md flex-col gap-2">
        {[0, 1, 2].map((i) => <div key={i} className="h-12 rounded-card bg-line-soft" />)}
      </div>
    )
  }
  if (projects.isError) {
    return <p role="alert" className="mt-20 text-center text-danger">Could not load projects.</p>
  }

  if (!projects.data?.length) {
    return (
      <div className="mx-auto mt-20 flex max-w-md flex-col items-center gap-4 text-center">
        <Brand markOnly size={40} />
        <h1 className="text-[22px] font-semibold tracking-tight">Your secrets, sealed and audited</h1>
        <p className="text-[13.5px] text-muted">
          Projects, environments and configs — encrypted end-to-end, every reveal audited.
        </p>
        <button
          onClick={() => setCreating(true)}
          className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card"
        >
          Create your first project
        </button>
        {createDialog}
      </div>
    )
  }

  return (
    <div className="mx-auto mt-20 flex w-full max-w-md flex-col gap-2">
      <h1 className="mb-1 text-[17px] font-semibold tracking-tight">Open a project</h1>
      {projects.data.map((p) => (
        <Link
          key={p.id}
          to={`/projects/${p.id}`}
          className="flex items-center justify-between rounded-card border border-line bg-card px-4 py-3 shadow-card hover:border-brand-line"
        >
          <span className="text-[13.5px] font-semibold">{p.name}</span>
          <span className="text-[11.5px] text-faint">{p.slug}</span>
        </Link>
      ))}
      <button
        onClick={() => setCreating(true)}
        className="mt-1 flex items-center justify-center gap-1.5 rounded border border-line bg-card px-4 py-2 text-[13px] font-semibold"
      >
        <Plus size={14} strokeWidth={1.7} /> New project
      </button>
      {createDialog}
    </div>
  )
}
