import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowRight, Eye, EyeOff, Lock } from 'lucide-react'
import { Modal } from '../ui/Modal'
import { Button } from '../ui/Button'
import { Pill, type Tone } from '../ui/Pill'
import { useToast } from '../ui/Toast'
import { cn } from '../ui/cn'
import { envTone, envDotClass } from '../ui/env'
import { errorMessage } from '../lib/api'
import { type Config, type Environment } from '../lib/endpoints'
import { promotion, type DiffEntry, type PromoteStatus, type Selection } from './endpoints'

// Status → chip presentation. `same` rows carry nothing to promote.
const CHIP: Record<PromoteStatus, { tone: Tone; label: string }> = {
  add: { tone: 'success', label: 'Add' },
  change: { tone: 'warning', label: 'Change' },
  remove: { tone: 'danger', label: 'Remove' },
  same: { tone: 'muted', label: 'Unchanged' },
}

// Default checked: add/change rows, UNLESS locked. remove/same/locked → unchecked.
function defaultChecked(e: DiffEntry): boolean {
  if (e.locked) return false
  return e.status === 'add' || e.status === 'change'
}

// `same` rows have nothing to promote; locked rows are server-rejected. Both are
// hard-disabled in the UI so a checked selection can never include them.
function isDisabled(e: DiffEntry): boolean {
  return e.locked || e.status === 'same'
}

const MASK = '••••••••'

function EnvTag({ env, label }: { env: Environment; label: string }) {
  const tone = envTone(env.slug)
  return (
    <span className="inline-flex items-center gap-1.5 text-[13px] font-semibold text-ink-hi">
      <span className={cn('h-2 w-2 rounded-full', envDotClass[tone])} />
      {env.name}
      <span className="text-[11px] font-normal text-ink-faint">{label}</span>
    </span>
  )
}

// A single from→to value cell pair. Values are secrets: masked until the row's
// reveal toggle is pressed. The preview endpoint is audited server-side; the
// mask here is display-only.
function ValueCell({ value, revealed, side }: { value: string; revealed: boolean; side: 'from' | 'to' }) {
  if (value === '') {
    return <span className="font-sans text-[11.5px] italic text-ink-faint">— absent —</span>
  }
  return (
    <span
      className={cn(
        'block overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[12px]',
        side === 'to' ? 'text-ink-mute' : 'text-ink-body',
      )}
      title={revealed ? value : undefined}
    >
      {revealed ? value : MASK}
    </span>
  )
}

function DiffRow({ entry, checked, onToggle }: { entry: DiffEntry; checked: boolean; onToggle: () => void }) {
  const [revealed, setRevealed] = useState(false)
  const chip = CHIP[entry.status]
  const disabled = isDisabled(entry)
  const wash =
    entry.status === 'add' ? 'bg-added-wash' : entry.status === 'remove' ? 'bg-removed-wash' : undefined

  return (
    <div
      data-row
      className={cn(
        'grid grid-cols-[34px_150px_1fr] items-center gap-2.5 rounded px-3 py-2 transition-nocturne hover:bg-row-hover',
        wash,
        entry.status === 'same' && 'opacity-60',
      )}
    >
      <div>
        <input
          type="checkbox"
          className="h-[18px] w-[18px] accent-brand disabled:cursor-not-allowed disabled:opacity-40"
          checked={checked}
          disabled={disabled}
          onChange={onToggle}
          aria-label={`Promote ${entry.key}`}
        />
      </div>
      <div className="min-w-0">
        <div className="flex items-center gap-1.5 overflow-hidden">
          <span className="overflow-hidden text-ellipsis whitespace-nowrap font-mono text-[12.5px] font-semibold text-ink-hi" title={entry.key}>
            {entry.key}
          </span>
          {entry.locked && <Lock size={11} strokeWidth={1.8} className="shrink-0 text-warning" aria-label="locked" />}
        </div>
        {entry.locked && <div className="mt-0.5 text-[10px] text-warning">locked in target</div>}
        <div className="mt-1">
          <Pill tone={chip.tone}>{chip.label}</Pill>
        </div>
      </div>
      <div className="grid min-w-0 grid-cols-[1fr_28px_1fr] items-center gap-2">
        <div className="flex min-w-0 items-center gap-1.5">
          {entry.source_value !== '' && (
            <button
              type="button"
              onClick={() => setRevealed((v) => !v)}
              aria-label={revealed ? `Hide ${entry.key}` : `Reveal ${entry.key} (audited)`}
              className="grid h-5 w-[22px] shrink-0 place-items-center rounded border border-line text-ink-faint transition-nocturne hover:border-brand-line hover:text-brand-text"
            >
              {revealed ? <EyeOff size={13} strokeWidth={1.7} /> : <Eye size={13} strokeWidth={1.7} />}
            </button>
          )}
          <ValueCell value={entry.source_value} revealed={revealed} side="from" />
        </div>
        <ArrowRight size={13} strokeWidth={1.7} className="justify-self-center text-ink-faint" aria-hidden />
        <ValueCell value={entry.target_value} revealed={revealed} side="to" />
      </div>
    </div>
  )
}

export function PromotionDiffModal({
  from,
  to,
  createName,
  fromEnv,
  toEnv,
  onClose,
  onDone,
}: {
  from: Config
  to?: Config // existing-target mode (as today) when provided
  createName?: string // create-target mode when `to` is undefined (the config name to create)
  fromEnv: Environment
  toEnv: Environment
  onClose: () => void
  onDone?: () => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const mode: 'existing' | 'create' = to ? 'existing' : 'create'
  // The config code shown in the header — the real target name, or the name we'll create.
  const targetName = to ? to.name : createName ?? ''

  // Existing-target diff: source→target, values revealable (audited server-side).
  const preview = useQuery({
    queryKey: ['promote-preview', from.id, to?.id],
    enabled: mode === 'existing',
    queryFn: () => promotion.preview(from.id, to!.id),
  })

  // Create-target diff: the backend synthesizes a preview against the not-yet-existing
  // target (all `add` entries, populated source_value, target_exists:false). Values are
  // revealable per-row exactly like existing mode (the endpoint is audited server-side).
  const createPreview = useQuery({
    queryKey: ['promote-create-preview', from.id, toEnv.id],
    enabled: mode === 'create',
    queryFn: () => promotion.previewCreate(from.id, toEnv.id),
  })

  // Unify the two modes into a single (entries, sourceVersion, loading/error) surface
  // so the selection + render code below is shared.
  const src = mode === 'existing' ? preview : createPreview
  const entries: DiffEntry[] = src.data?.entries ?? []
  const sourceVersion = src.data?.source_version
  const isLoading = src.isLoading
  const isError = src.isError
  const error = src.error
  const hasData = sourceVersion !== undefined

  // Per-key checked state, keyed by entry key. Seeded once from the loaded diff.
  const [checked, setChecked] = useState<Record<string, boolean> | null>(null)
  const selection = useMemo<Record<string, boolean>>(() => {
    if (checked) return checked
    const seed: Record<string, boolean> = {}
    for (const e of entries) seed[e.key] = defaultChecked(e)
    return seed
  }, [checked, entries])

  const selectedCount = entries.filter((e) => selection[e.key] && !isDisabled(e)).length
  const total = entries.length

  function toggle(e: DiffEntry) {
    if (isDisabled(e)) return
    setChecked((prev) => {
      const base = prev ?? Object.fromEntries(entries.map((x) => [x.key, defaultChecked(x)]))
      return { ...base, [e.key]: !base[e.key] }
    })
  }

  const apply = useMutation({
    mutationFn: () => {
      const selections: Selection[] = entries
        .filter((e) => selection[e.key] && !isDisabled(e))
        .map((e) => ({ key: e.key, action: e.status === 'remove' ? 'remove' : 'set' }))
      return promotion.apply(
        mode === 'existing'
          ? {
              from_config: from.id,
              to_config: to!.id,
              source_version: sourceVersion!,
              selections,
            }
          : {
              from_config: from.id,
              to_env: toEnv.id,
              to_name: createName!,
              create: true,
              source_version: sourceVersion!,
              selections,
            },
      )
    },
    onSuccess: (res) => {
      if (mode === 'existing') {
        toast({ title: `Promoted ${res.applied.length} key${res.applied.length === 1 ? '' : 's'} to ${toEnv.name}`, tone: 'success' })
        // The secret editor keys the target config on ['config', <configId>].
        void qc.invalidateQueries({ queryKey: ['config', to!.id] })
      } else {
        toast({ title: `Created ${createName} in ${toEnv.name} with ${res.applied.length} key${res.applied.length === 1 ? '' : 's'}`, tone: 'success' })
        // Refresh every board config list so the newly-created config appears.
        void qc.invalidateQueries({ queryKey: ['configs'] })
      }
      onDone?.()
      onClose()
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  return (
    <Modal open onClose={onClose} label={`Promote ${fromEnv.name} to ${toEnv.name}`} className="flex max-h-[88vh] w-[min(720px,92vw)] flex-col p-0">
      <div className="border-b border-line-soft px-5 pb-3.5 pt-4">
        <div className="flex flex-wrap items-center gap-2.5">
          <EnvTag env={fromEnv} label="source" />
          <ArrowRight size={15} strokeWidth={1.7} className="text-ink-faint" aria-hidden />
          <EnvTag env={toEnv} label="target" />
          <span className="text-[13px] font-medium text-ink-mute">
            · config <code className="rounded border border-line-soft bg-card px-1.5 font-mono text-[11.5px] text-ink-body">{targetName}</code>
          </span>
        </div>
        <p className="mt-2 text-[12px] text-ink-mute">
          Applies checked keys to <b className="text-ink-body">{toEnv.name}</b> as a new version.
          Unchecked keys keep {toEnv.name} as-is.
        </p>
        {(mode === 'create' || (preview.data && !preview.data.target_exists)) && (
          <div className="mt-2.5 flex items-start gap-2 rounded-sm border border-line-soft bg-info-soft px-2.5 py-2 text-[11.5px] text-info">
            <span aria-hidden>✨</span>
            <span><b>{toEnv.name}</b> has no <code className="font-mono">{targetName}</code> config yet — promoting will create it.</span>
          </div>
        )}
      </div>

      <div className="overflow-auto px-2 pb-2 pt-1.5">
        {isLoading && <p className="px-3 py-6 text-[12.5px] text-ink-mute">Loading diff…</p>}
        {isError && (
          <p role="alert" className="px-3 py-6 text-[12.5px] text-danger">{errorMessage(error, "Couldn't load the diff.")}</p>
        )}
        {hasData && entries.length === 0 && (
          <p className="px-3 py-6 text-[12.5px] text-ink-mute">
            {mode === 'create' ? 'Nothing to promote — the source config has no keys.' : 'Nothing to promote — the configs already match.'}
          </p>
        )}
        {hasData &&
          entries.map((e) => (
            <DiffRow key={e.key} entry={e} checked={!!selection[e.key]} onToggle={() => toggle(e)} />
          ))}
      </div>

      <div className="flex items-center gap-1.5 px-5 pb-3 text-[11px] text-ink-faint">
        <span aria-hidden>🔎</span>
        <span>Revealing a value writes an audit event — names &amp; counts are always logged, values never.</span>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3.5 border-t border-line-soft px-5 py-3">
        <span className="text-[12px] text-ink-mute">
          <b className="text-ink-body tabular-nums">{selectedCount}</b> of{' '}
          <b className="text-ink-body tabular-nums">{total}</b> keys selected
        </span>
        <div className="flex items-center gap-2">
          <Button variant="secondary" size="sm" onClick={onClose}>Cancel</Button>
          <Button
            size="sm"
            loading={apply.isPending}
            disabled={selectedCount === 0 || !hasData}
            onClick={() => apply.mutate()}
          >
            {mode === 'create' ? 'Create' : 'Promote'} {selectedCount} key{selectedCount === 1 ? '' : 's'} →
          </Button>
        </div>
      </div>
    </Modal>
  )
}
