import { FormEvent, useState } from 'react'
import { Modal } from '../ui/Modal'
import { Input } from '../ui/Input'
import { Button } from '../ui/Button'

export function CloneEnvDialog({ initial, onSubmit, onClose }: {
  initial?: { slug?: string; name?: string }
  onSubmit: (slug: string, name: string) => void
  onClose: () => void
}) {
  const [slug, setSlug] = useState(initial?.slug ?? '')
  const [name, setName] = useState(initial?.name ?? '')
  const disabled = slug.trim() === ''

  function submit(e: FormEvent) {
    e.preventDefault()
    if (disabled) return
    onSubmit(slug.trim(), name.trim())
  }

  return (
    <Modal open onClose={onClose} label="Clone environment" className="w-80">
      <h2 className="mb-3 text-[15px] font-semibold tracking-tight text-ink">Clone environment</h2>
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Input
          label="Slug"
          value={slug}
          onChange={(e) => setSlug(e.target.value)}
          autoFocus
          className="font-mono"
        />
        <Input
          label="Name"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <div className="mt-1 flex justify-end gap-2">
          <Button type="button" variant="secondary" size="sm" onClick={onClose}>Cancel</Button>
          <Button type="submit" size="sm" disabled={disabled}>Save</Button>
        </div>
      </form>
    </Modal>
  )
}
