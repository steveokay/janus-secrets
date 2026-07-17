import { FormEvent, useState } from 'react'
import { Modal } from '../ui/Modal'
import { Input } from '../ui/Input'
import { Button } from '../ui/Button'

export function RenameDialog({ title, initial, onSubmit, onClose }: {
  title: string
  initial: string
  onSubmit: (name: string) => void
  onClose: () => void
}) {
  const [name, setName] = useState(initial)
  const trimmed = name.trim()
  const disabled = trimmed === '' || trimmed === initial

  function submit(e: FormEvent) {
    e.preventDefault()
    if (disabled) return
    onSubmit(trimmed)
  }

  return (
    <Modal open onClose={onClose} label={title} className="w-80">
      <h2 className="mb-3 text-[15px] font-semibold tracking-tight text-ink">{title}</h2>
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Input
          label="Name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoFocus
        />
        <div className="mt-1 flex justify-end gap-2">
          <Button type="button" variant="secondary" size="sm" onClick={onClose}>Cancel</Button>
          <Button type="submit" size="sm" disabled={disabled}>Save</Button>
        </div>
      </form>
    </Modal>
  )
}
