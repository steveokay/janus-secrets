import { FormEvent, useState, ReactNode } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints, Project, Environment, Config } from '../lib/endpoints'
import { ApiError } from '../lib/api'

function Dialog({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/30">
      <div className="w-80 rounded bg-white p-4 shadow">
        <h2 className="mb-3 text-lg font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  )
}

function useSubmit<T>(fn: () => Promise<T>, onDone: (v: T) => void) {
  const [error, setError] = useState('')
  const m = useMutation({
    mutationFn: fn,
    onSuccess: onDone,
    onError: (e) => setError(e instanceof ApiError ? e.message : 'Failed to create.'),
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
  )
  return (
    <Dialog title="New project">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <label className="text-sm">Slug<input aria-label="slug" value={slug} onChange={(e) => setSlug(e.target.value)} required className="w-full rounded border p-1" /></label>
        <label className="text-sm">Name<input aria-label="name" value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border p-1" /></label>
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border px-2 py-1">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-2 py-1 text-white disabled:opacity-50">Create</button>
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
  )
  return (
    <Dialog title="New environment">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <label className="text-sm">Slug<input aria-label="slug" value={slug} onChange={(e) => setSlug(e.target.value)} required className="w-full rounded border p-1" /></label>
        <label className="text-sm">Name<input aria-label="name" value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border p-1" /></label>
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border px-2 py-1">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-2 py-1 text-white disabled:opacity-50">Create</button>
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
  )
  return (
    <Dialog title="New config">
      <form onSubmit={submit} className="flex flex-col gap-2">
        <label className="text-sm">Name<input aria-label="name" value={name} onChange={(e) => setName(e.target.value)} required className="w-full rounded border p-1" /></label>
        <label className="text-sm">Inherits from (same environment, optional)
          <select aria-label="inherits from" value={base} onChange={(e) => setBase(e.target.value)} className="w-full rounded border p-1">
            <option value="">— none —</option>
            {bases.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
        </label>
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border px-2 py-1">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-blue-600 px-2 py-1 text-white disabled:opacity-50">Create</button>
        </div>
      </form>
    </Dialog>
  )
}
