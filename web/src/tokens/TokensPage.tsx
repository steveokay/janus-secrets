import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { endpoints, TokenMeta, MintTokenResult } from '../lib/endpoints'
import { ApiError, apiErrorTitle } from '../lib/api'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'
import { Pill, Tone } from '../ui/Pill'
import { Sheet } from '../ui/Sheet'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { EmptyState } from '../ui/EmptyState'
import { RevealOnce } from '../ui/RevealOnce'
import { useToast } from '../ui/Toast'
import { useTitle } from '../lib/title'
import { timeAgo } from '../lib/time'

type ScopeKind = TokenMeta['scope_kind']

const kindTone: Record<ScopeKind, Tone> = { config: 'brand', environment: 'info', transit: 'muted' }

// Best-effort scope name resolution over the nav query caches (populated by
// the sidebar/breadcrumb as the user browses). Falls back to a truncated id
// when the relevant project/env/config was never loaded into cache.
function useResolvedScopeName(kind: ScopeKind, id: string): string {
  const qc = useQueryClient()
  if (kind === 'transit') return id ? id.slice(0, 8) : 'all keys'
  const cacheKey = kind === 'config' ? 'configs' : 'envs'
  const queries = qc.getQueriesData<{ id: string; name: string }[]>({ queryKey: [cacheKey] })
  for (const [, data] of queries) {
    const match = data?.find((x) => x.id === id)
    if (match) return match.name
  }
  return id.slice(0, 8)
}

function ScopeCell({ kind, id }: { kind: ScopeKind; id: string }) {
  const name = useResolvedScopeName(kind, id)
  return (
    <div className="flex items-center gap-1.5">
      <Pill tone={kindTone[kind]}>{kind}</Pill>
      <span className="text-[12px] text-muted">{name}</span>
    </div>
  )
}

function MintTokenSheet({ onClose, onMinted }: {
  onClose: () => void
  onMinted: (r: MintTokenResult) => void
}) {
  const toast = useToast()
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [kind, setKind] = useState<ScopeKind>('config')
  const [pid, setPid] = useState('')
  const [eid, setEid] = useState('')
  const [cid, setCid] = useState('')
  const [access, setAccess] = useState('read')
  const [ttl, setTtl] = useState('')

  const projects = useProjects()
  const envs = useEnvironments(kind !== 'transit' ? pid || undefined : undefined)
  const configs = useConfigs(
    kind === 'config' ? pid || undefined : undefined,
    kind === 'config' ? eid || undefined : undefined,
  )

  const accessOptions = kind === 'transit' ? ['use', 'manage'] : ['read', 'readwrite']

  function handleKindChange(k: ScopeKind) {
    setKind(k)
    setAccess(k === 'transit' ? 'use' : 'read')
    setPid('')
    setEid('')
    setCid('')
  }

  const scopeId = kind === 'transit' ? '' : kind === 'environment' ? eid : cid
  const canSubmit = name.trim() !== '' && (kind === 'transit' || (kind === 'environment' ? !!eid : !!cid))

  const mutation = useMutation({
    mutationFn: () => endpoints.mintToken({
      name,
      scope: { kind, id: scopeId },
      access,
      ...(ttl ? { ttl_seconds: Number(ttl) } : {}),
    }),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: ['tokens'] })
      // Neutral confirmation only — the token value is shown once via RevealOnce,
      // NEVER in a toast title.
      toast({ title: 'Token created' })
      onMinted(r)
    },
    onError: (e) => toast({ title: apiErrorTitle(e), tone: 'danger' }),
  })

  return (
    <Sheet open onOpenChange={(o) => { if (!o) onClose() }} title="Mint token">
      <form
        onSubmit={(e) => { e.preventDefault(); mutation.mutate() }}
        className="flex flex-col gap-3"
      >
        <label className="text-[12px] font-semibold">
          Name
          <input
            aria-label="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            className="mt-1 w-full rounded border border-line px-3 py-2 text-[13px] font-normal"
          />
        </label>
        <label className="text-[12px] font-semibold">
          Kind
          <select
            aria-label="kind"
            value={kind}
            onChange={(e) => handleKindChange(e.target.value as ScopeKind)}
            className="mt-1 w-full rounded border border-line px-3 py-2 text-[13px] font-normal"
          >
            <option value="config">config</option>
            <option value="environment">environment</option>
            <option value="transit">transit</option>
          </select>
        </label>
        {kind !== 'transit' && (
          <>
            <label className="text-[12px] font-semibold">
              Project
              <select
                aria-label="project"
                value={pid}
                onChange={(e) => { setPid(e.target.value); setEid(''); setCid('') }}
                className="mt-1 w-full rounded border border-line px-3 py-2 text-[13px] font-normal"
              >
                <option value="">— select —</option>
                {(projects.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </label>
            <label className="text-[12px] font-semibold">
              Environment
              <select
                aria-label="environment"
                value={eid}
                onChange={(e) => { setEid(e.target.value); setCid('') }}
                disabled={!pid}
                className="mt-1 w-full rounded border border-line px-3 py-2 text-[13px] font-normal disabled:opacity-50"
              >
                <option value="">— select —</option>
                {(envs.data ?? []).map((e) => <option key={e.id} value={e.id}>{e.name}</option>)}
              </select>
            </label>
          </>
        )}
        {kind === 'config' && (
          <label className="text-[12px] font-semibold">
            Config
            <select
              aria-label="config"
              value={cid}
              onChange={(e) => setCid(e.target.value)}
              disabled={!eid}
              className="mt-1 w-full rounded border border-line px-3 py-2 text-[13px] font-normal disabled:opacity-50"
            >
              <option value="">— select —</option>
              {(configs.data ?? []).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </label>
        )}
        <label className="text-[12px] font-semibold">
          Access
          <select
            aria-label="access"
            value={access}
            onChange={(e) => setAccess(e.target.value)}
            className="mt-1 w-full rounded border border-line px-3 py-2 text-[13px] font-normal"
          >
            {accessOptions.map((a) => <option key={a} value={a}>{a}</option>)}
          </select>
        </label>
        <label className="text-[12px] font-semibold">
          TTL seconds (optional)
          <input
            aria-label="ttl seconds"
            type="number"
            min={1}
            value={ttl}
            onChange={(e) => setTtl(e.target.value)}
            className="mt-1 w-full rounded border border-line px-3 py-2 text-[13px] font-normal"
          />
        </label>
        {mutation.isError && (
          <p role="alert" className="text-[12.5px] text-danger">{apiErrorTitle(mutation.error)}</p>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!canSubmit || mutation.isPending}
            className="rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white disabled:opacity-50"
          >
            Mint
          </button>
        </div>
      </form>
    </Sheet>
  )
}

export function TokensPage() {
  useTitle('Service tokens')
  const qc = useQueryClient()
  const toast = useToast()
  const tokens = useQuery({ queryKey: ['tokens'], queryFn: endpoints.listTokens })
  const [mintOpen, setMintOpen] = useState(false)
  const [minted, setMinted] = useState<MintTokenResult | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<TokenMeta | null>(null)

  const revoke = useMutation({
    mutationFn: (id: string) => endpoints.revokeToken(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['tokens'] })
      toast({ title: 'Token revoked' })
      setRevokeTarget(null)
    },
    onError: (e) => {
      toast({ title: apiErrorTitle(e), tone: 'danger' })
      setRevokeTarget(null)
    },
  })

  const forbidden = tokens.error instanceof ApiError && tokens.error.status === 403
  const rows = tokens.data ?? []

  const mintButton = (
    <button
      type="button"
      onClick={() => setMintOpen(true)}
      className="rounded bg-brand px-3 py-1.5 text-[12.5px] font-semibold text-white shadow-card"
    >
      Mint token
    </button>
  )

  return (
    <div>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-[15px] font-semibold text-ink">Service tokens</h3>
          <p className="text-[12.5px] text-faint">Scoped credentials for CI and automation</p>
        </div>
        {mintButton}
      </div>

      {forbidden ? (
        <EmptyState title="Token access required" hint="Ask an instance admin or owner for access." />
      ) : tokens.isError ? (
        <p role="alert" className="text-[12.5px] text-danger">Couldn't load tokens.</p>
      ) : tokens.isLoading ? (
        <div className="flex flex-col gap-1.5" aria-hidden="true">
          {[0, 1, 2].map((i) => <div key={i} className="h-8 animate-pulse rounded bg-line-soft" />)}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          title="No service tokens yet"
          hint="Mint a scoped token so CI and automation can authenticate."
          action={mintButton}
        />
      ) : (
        <table className="w-full overflow-hidden rounded-card border border-line bg-card text-sm shadow-card">
          <thead>
            <tr className="text-left text-[10.5px] uppercase tracking-[.1em] text-faint">
              <th className="py-1.5">Name</th>
              <th className="py-1.5">Scope</th>
              <th className="py-1.5">Access</th>
              <th className="py-1.5">Created</th>
              <th className="py-1.5">Expires</th>
              <th className="py-1.5">Status</th>
              <th className="py-1.5" />
            </tr>
          </thead>
          <tbody>
            {rows.map((t) => (
              <tr key={t.id} className="border-t border-line-soft">
                <td className="py-1.5">{t.name}</td>
                <td className="py-1.5"><ScopeCell kind={t.scope_kind} id={t.scope_id} /></td>
                <td className="py-1.5">{t.access}</td>
                <td className="py-1.5"><span title={t.created_at}>{timeAgo(t.created_at)}</span></td>
                <td className="py-1.5">
                  {t.expires_at ? <span title={t.expires_at}>{timeAgo(t.expires_at)}</span> : 'never'}
                </td>
                <td className="py-1.5">{t.revoked_at ? <Pill tone="danger">revoked</Pill> : null}</td>
                <td className="py-1.5 text-right">
                  {!t.revoked_at && (
                    <button
                      type="button"
                      onClick={() => setRevokeTarget(t)}
                      className="text-[12.5px] font-semibold text-danger hover:underline"
                    >
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {mintOpen && (
        <MintTokenSheet
          onClose={() => setMintOpen(false)}
          onMinted={(r) => { setMintOpen(false); setMinted(r) }}
        />
      )}

      {minted && (
        <RevealOnce
          open
          onClose={() => setMinted(null)}
          title="Service token"
          secret={minted.token}
          hint="Shown once — clients authenticate with it via the Authorization header."
        />
      )}

      {revokeTarget && (
        <ConfirmDialog
          open
          onOpenChange={(o) => { if (!o) setRevokeTarget(null) }}
          title={`Revoke ${revokeTarget.name}?`}
          body="Clients using it stop working immediately."
          confirmLabel="Revoke"
          tone="danger"
          onConfirm={() => revoke.mutate(revokeTarget.id)}
        />
      )}
    </div>
  )
}
