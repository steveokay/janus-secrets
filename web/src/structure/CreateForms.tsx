import { FormEvent, useState, ReactNode } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints, Project, Environment, Config } from '../lib/endpoints'
import { errorMessage } from '../lib/api'
import { useToast } from '../ui/Toast'
import { Input } from '../ui/Input'
import { Select } from '../ui/Select'
import { Button } from '../ui/Button'

function Dialog({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30">
      <div className="w-80 rounded-card border border-line bg-card p-5 shadow-pop">
        <h2 className="mb-3 text-[15px] font-semibold tracking-tight">{title}</h2>
        {children}
      </div>
    </div>
  )
}

// Shared create-form controller. Confirms success with a toast; failures stay
// INLINE (curated errorMessage) so validation/conflict text stays on screen next
// to the field — no jarring duplicate danger toast for the same failure.
function useSubmit<T>(fn: () => Promise<T>, onDone: (v: T) => void, successTitle: string) {
  const toast = useToast()
  const [error, setError] = useState('')
  const m = useMutation({
    mutationFn: fn,
    onSuccess: (v) => { toast({ title: successTitle }); onDone(v) },
    onError: (e) => setError(errorMessage(e, 'Failed to create.')),
  })
  return { error, submit: (e: FormEvent) => { e.preventDefault(); setError(''); m.mutate() }, busy: m.isPending }
}

export function CreateProjectForm({ onCreated, onClose }: { onCreated: (p: Project) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const [slug, setSlug] = useState('')
  const [name, setName] = useState('')
  const { error, submit, busy } = useSubmit(
    () => endpoints.createProject(slug, name),
    (p) => { void qc.invalidateQueries({ queryKey: ['projects'] }); onCreated(p) },
    'Project created',
  )
  return (
    <Dialog title="Create project">
      <p className="mb-3 text-[12.5px] leading-relaxed text-ink-mute">
        Group your Development, Staging, and Production secrets. Each project holds
        multiple configs with versioned history and per-environment access.
      </p>
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Input
          label="Name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. api-gateway"
          required
        />
        <Input
          label="Slug"
          value={slug}
          onChange={(e) => setSlug(e.target.value)}
          placeholder="api-gateway"
          required
          className="font-mono"
        />
        {error && <p role="alert" className="text-[12.5px] text-danger">{error}</p>}
        <div className="mt-1 flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" loading={busy}>Create project</Button>
        </div>
      </form>
    </Dialog>
  )
}

export function CreateEnvironmentForm({ pid, onCreated, onClose }: { pid: string; onCreated: (e: Environment) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const [slug, setSlug] = useState('')
  const [name, setName] = useState('')
  const { error, submit, busy } = useSubmit(
    () => endpoints.createEnvironment(pid, slug, name),
    (e) => { void qc.invalidateQueries({ queryKey: ['envs', pid] }); onCreated(e) },
    'Environment created',
  )
  return (
    <Dialog title="New environment">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <Input label="Slug" value={slug} onChange={(e) => setSlug(e.target.value)} required />
        <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} required />
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" loading={busy}>Create</Button>
        </div>
      </form>
    </Dialog>
  )
}

export function CreateConfigForm({ pid, eid, bases, onCreated, onClose }: { pid: string; eid: string; bases: Config[]; onCreated: (c: Config) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [base, setBase] = useState('')
  const { error, submit, busy } = useSubmit(
    () => endpoints.createConfig(pid, eid, name, base || undefined),
    (c) => { void qc.invalidateQueries({ queryKey: ['configs', pid, eid] }); onCreated(c) },
    'Config created',
  )
  return (
    <Dialog title="New config">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} required />
        <Select label="Inherits from (same environment, optional)" value={base} onChange={(e) => setBase(e.target.value)}>
          <option value="">— none —</option>
          {bases.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
        </Select>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" loading={busy}>Create</Button>
        </div>
      </form>
    </Dialog>
  )
}
