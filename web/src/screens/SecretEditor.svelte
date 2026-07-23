<script lang="ts">
  import { registry } from '../lib/registry.svelte'
  import { api, errorMessage, type VersionMeta, type VersionDiff, type SecretChange, type KeyVersionMeta, type KeyReadInsight } from '../lib/api'
  import { relTime, stampDate, isValidKey, isEnvVarKey, parseEnvOrProps, humanizeDuration, parseDurationToSeconds, type ImportedEntry } from '../lib/util'
  import { checkFormat, prettyJson } from '../lib/format'
  import { router } from '../lib/router.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import PromotePanel from '../components/PromotePanel.svelte'
  import GenerateMenu from '../components/GenerateMenu.svelte'
  import Sparkline from '../components/Sparkline.svelte'
  import NotFound from './NotFound.svelte'

  let { projectId, configId }: { projectId: string; configId: string } = $props()

  const ctx = $derived(registry.findConfig(configId))

  interface Row {
    key: string
    origin: 'own' | 'inherited' | 'overridden'
    type?: string          // declared secret type (json/certificate/…) — a display hint
    valueVersion: number
    createdAt: string
    maxAgeSeconds: number | null  // effective advisory max-age (null = no policy)
    stale: boolean                // past effective max-age (advisory; never blocks)
    lastReadAt: string | null     // most recent per-key reveal (null = never read)
    unused: boolean               // not read within the unused window (advisory)
    owner: string | null          // annotation owner label (null = unset; advisory)
    note: string | null           // annotation free-text note (null = unset; advisory)
    revealed: boolean
    value: string | null   // plaintext once revealed
    draft: string
    dirty: boolean
    deleted: boolean
    added: boolean
    editing: boolean
  }

  let rows = $state<Row[]>([])
  let versions = $state<VersionMeta[]>([])
  let userEmails = $state<Map<string, string>>(new Map())
  let diffs = $state<Record<number, VersionDiff>>({})
  let latestVersion = $state(0)
  let loading = $state(true)
  let loadError = $state('')
  let filter = $state('')
  let showVersions = $state(false)
  let showPromote = $state(false)
  let showImport = $state(false)
  let importText = $state('')
  let importPicked = $state<Record<number, boolean>>({})
  let lockedKeys = $state<Set<string>>(new Set())
  // Advisory max-age policy: keys with an explicit per-key override, and the
  // config-level default (seconds, or null when unset). Never blocks anything.
  let overriddenKeys = $state<Set<string>>(new Set())
  let configDefaultMaxAge = $state<number | null>(null)
  let maxAgeKeyFor = $state<string | null>(null)   // key whose max-age popover is open
  let maxAgeDraft = $state('')
  let showConfigMaxAge = $state(false)
  let configMaxAgeDraft = $state('')
  // Per-key annotation (owner + note; value-free, never blocks). Popover editor
  // keyed by secret name; drafts hold the in-progress owner/note.
  let annotationKeyFor = $state<string | null>(null)
  let annotationOwnerDraft = $state('')
  let annotationNoteDraft = $state('')
  let historyFor = $state<string | null>(null)
  let keyHistory = $state<KeyVersionMeta[]>([])
  let historicValues = $state<Record<number, string>>({})
  let savedFlash = $state<number | null>(null)
  let toast = $state('')
  let saveError = $state('')
  let saving = $state(false)
  // Per-key advisory read insights (value-free: last-read + 30-day daily
  // sparkline). Lazy-loaded once per editor open; keyed by secret name. Keys
  // never revealed per-key are absent → treated as "never read".
  let insights = $state<Record<string, KeyReadInsight>>({})
  let insightsWindow = $state(30)
  let insightsFor = $state<string | null>(null)   // key whose insights panel is open
  // Bulk row selection (Part A). Tracks selected key NAMES; actions operate on
  // the selection intersected with the current filter. Cleared after any action
  // and on (re)load / save.
  let selected = $state<Set<string>>(new Set())

  $effect(() => {
    void load(configId)
  })

  async function load(cid: string) {
    loading = true
    loadError = ''
    rows = []
    versions = []
    diffs = {}
    showVersions = false
    showPromote = false
    historyFor = null
    insightsFor = null
    insights = {}
    selected = new Set()
    // Deep-link: the command palette navigates here with ?key=<name> to
    // pre-filter the editor to that key. Harmless metadata (a key name); if
    // absent the filter stays empty.
    filter = new URLSearchParams(window.location.search).get('key') ?? ''
    try {
      const [masked, vers, users, locked, maxAge, ins] = await Promise.all([
        api.maskedSecrets(cid),
        api.listVersions(cid),
        api.listUsers().catch(() => []),
        api.listLockedKeys(cid).catch(() => [] as string[]),
        api.listMaxAge(cid).catch(() => []),
        // Advisory read insights ride secret:read; a viewer without it, or an
        // error, simply leaves the sparkline/last-read absent (decorative).
        api.readInsights(cid).catch(() => ({ window_days: 30, keys: {} })),
      ])
      insights = ins.keys
      insightsWindow = ins.window_days || 30
      lockedKeys = new Set(locked)
      overriddenKeys = new Set(maxAge.filter(p => p.key !== '').map(p => p.key))
      configDefaultMaxAge = maxAge.find(p => p.key === '')?.max_age_seconds ?? null
      userEmails = new Map(users.map(u => [u.id, u.email]))
      versions = vers.slice().sort((a, b) => b.version - a.version)
      latestVersion = versions[0]?.version ?? 0
      rows = Object.entries(masked)
        .map(([key, m]) => ({
          key,
          origin: m.origin,
          type: m.type,
          valueVersion: m.value_version,
          createdAt: m.created_at,
          maxAgeSeconds: m.max_age_seconds ?? null,
          stale: m.stale ?? false,
          lastReadAt: m.last_read_at ?? null,
          unused: m.unused ?? false,
          owner: m.owner ?? null,
          note: m.note ?? null,
          revealed: false,
          value: null,
          draft: '',
          dirty: false,
          deleted: false,
          added: false,
          editing: false,
        }))
        .sort((a, b) => a.key.localeCompare(b.key))
    } catch (err) {
      loadError = errorMessage(err, 'Could not open this config.')
    } finally {
      loading = false
    }
  }

  const visible = $derived(rows.filter(r => r.key.toLowerCase().includes(filter.toLowerCase())))
  const dirtyCount = $derived(rows.filter(r => r.dirty || r.deleted || r.added).length)
  const anyHidden = $derived(rows.some(r => !r.revealed && !r.added))
  const badKeys = $derived(rows.filter(r => r.added && r.key.trim() !== '' && !isValidKey(r.key.trim())).length)

  // ── bulk row selection (Part A) ───────────────────────────────────────────
  // Only persisted rows are selectable (added-but-unsaved rows have no stable
  // key yet). Selection is by key name; all bulk actions operate on the visible
  // selection so a filter narrows the target set.
  const selectableVisible = $derived(visible.filter(r => !r.added && r.key))
  const visibleSelected = $derived(selectableVisible.filter(r => selected.has(r.key)))
  const selCount = $derived(visibleSelected.length)
  const allVisibleSelected = $derived(selectableVisible.length > 0 && selCount === selectableVisible.length)

  function toggleRowSelect(key: string) {
    if (selected.has(key)) selected.delete(key)
    else selected.add(key)
    selected = new Set(selected)
  }

  function toggleSelectAll() {
    if (allVisibleSelected) {
      for (const r of selectableVisible) selected.delete(r.key)
    } else {
      for (const r of selectableVisible) selected.add(r.key)
    }
    selected = new Set(selected)
  }

  function clearSelection() {
    selected = new Set()
  }

  async function bulkDelete() {
    const targets = visibleSelected
    if (!targets.length) return
    const ok = await dialog.confirm({
      title: `Delete ${targets.length} selected key${targets.length === 1 ? '' : 's'}?`,
      body: 'Staged into the draft as deletions — nothing is removed until you Save. Deletes are soft and recoverable from Trash.',
      confirmLabel: 'Stage deletions',
      danger: true,
    })
    if (!ok) return
    let staged = 0
    for (const r of targets) {
      if (r.added) { rows = rows.filter(x => x !== r); continue } // defensive (not selectable)
      if (!r.deleted) { r.deleted = true; staged++ }
    }
    clearSelection()
    flashToast(`${staged} deletion${staged === 1 ? '' : 's'} staged into the draft — review, then Save`)
  }

  async function bulkReveal() {
    const targets = visibleSelected.filter(r => !r.revealed)
    if (!targets.length) { flashToast('Selected keys are already revealed.'); clearSelection(); return }
    await Promise.all(targets.map(async r => {
      try {
        const res = await api.revealKey(configId, r.key)
        r.value = res.value
        r.draft = res.value
        r.revealed = true
      } catch { /* per-key failure leaves the row masked */ }
    }))
    flashToast(`Reveal of ${targets.length} selected key${targets.length === 1 ? '' : 's'} recorded in the audit ledger`)
    clearSelection()
  }

  async function bulkExportEnv() {
    if (!ctx) return
    const targets = visibleSelected
    if (!targets.length) return
    const ok = await dialog.confirm({
      title: `Download ${targets.length} selected value${targets.length === 1 ? '' : 's'} as .env?`,
      body: `The ${targets.length} selected value${targets.length === 1 ? ' is' : 's are'} revealed (each recorded in the audit ledger) and written to a plaintext file on this machine.`,
      confirmLabel: 'Reveal & download',
      danger: true,
    })
    if (!ok) return
    try {
      const revealed = await Promise.all(
        targets.map(async r => {
          if (r.revealed && r.value !== null) return { key: r.key, value: r.value }
          const res = await api.revealKey(configId, r.key)
          return { key: r.key, value: res.value }
        }),
      )
      const lines: string[] = [
        `# ${ctx.project.name} / ${ctx.env.slug} / ${ctx.config.name} — ${revealed.length} selected keys exported from Janus (v${latestVersion})`,
      ]
      for (const { key, value } of revealed.sort((a, b) => a.key.localeCompare(b.key))) {
        if (isEnvVarKey(key)) lines.push(`${key}=${envQuote(value)}`)
        else lines.push(`# skipped (not an env-var name — use janus secrets download --format files): ${key}`)
      }
      const blob = new Blob([lines.join('\n') + '\n'], { type: 'text/plain' })
      const url = URL.createObjectURL(blob)
      try {
        const a = document.createElement('a')
        a.href = url
        a.download = `${ctx.project.name}-${ctx.env.slug}-${ctx.config.name}-selected.env`
        document.body.appendChild(a)
        a.click()
        a.remove()
      } finally {
        URL.revokeObjectURL(url)
      }
      flashToast(`Downloaded ${revealed.length} selected value${revealed.length === 1 ? '' : 's'} — every reveal recorded in the audit ledger`)
      clearSelection()
    } catch (err) {
      flashToast(errorMessage(err, 'Export failed.'))
    }
  }

  function toggleInsights(key: string) {
    insightsFor = insightsFor === key ? null : key
  }

  function flashToast(msg: string) {
    toast = msg
    setTimeout(() => (toast = ''), 3200)
  }

  async function reveal(row: Row) {
    try {
      const res = await api.revealKey(configId, row.key)
      row.value = res.value
      row.draft = res.value
      row.revealed = true
      flashToast(`Reveal of ${row.key} recorded in the audit ledger`)
    } catch (err) {
      flashToast(errorMessage(err, `Could not reveal ${row.key}.`))
    }
  }

  async function revealAll() {
    const hidden = rows.filter(r => !r.revealed && !r.added)
    await Promise.all(hidden.map(async r => {
      try {
        const res = await api.revealKey(configId, r.key)
        r.value = res.value
        r.draft = res.value
        r.revealed = true
      } catch { /* per-key failures already leave the row masked */ }
    }))
    flashToast(`Bulk reveal (${hidden.length} keys) recorded in the audit ledger`)
  }

  async function beginEdit(row: Row) {
    if (row.deleted) return
    if (!row.revealed) await reveal(row)
    if (row.revealed) row.editing = true
  }

  function commitEdit(row: Row) {
    row.editing = false
    row.dirty = row.added || row.draft !== (row.value ?? '')
  }

  // Recompute a row's dirty flag after its draft changes (e.g. the value
  // generator writes into row.draft). Keeps the row editing/revealed so the
  // user sees the generated value in the normal value input.
  function markDirty(row: Row) {
    row.dirty = row.added || row.draft !== (row.value ?? '')
  }

  function applyGenerated(row: Row, value: string) {
    row.draft = value
    row.revealed = true
    row.editing = true
    markDirty(row)
  }

  function toggleDelete(row: Row) {
    if (row.added) {
      rows = rows.filter(r => r !== row)
      return
    }
    row.deleted = !row.deleted
  }

  function addRow() {
    rows.push({
      key: '', origin: 'own', valueVersion: 0, createdAt: new Date().toISOString(),
      maxAgeSeconds: null, stale: false, lastReadAt: null, unused: false, owner: null, note: null,
      revealed: true, value: null, draft: '', dirty: false, deleted: false, added: true, editing: true,
    })
  }

  function discard() {
    void load(configId)
  }

  async function saveAll() {
    saveError = ''
    const changes: SecretChange[] = []
    for (const r of rows) {
      if (r.deleted) changes.push({ key: r.key, delete: true })
      else if ((r.dirty || r.added) && isValidKey(r.key.trim())) changes.push({ key: r.key.trim(), value: r.draft })
    }
    if (!changes.length) return
    saving = true
    try {
      const res = await api.saveSecrets(configId, changes, `Saved from the Atrium editor`)
      savedFlash = res.version
      setTimeout(() => (savedFlash = null), 2600)
      clearSelection()
      await load(configId)
      await registry.hydrate(true)
    } catch (err) {
      saveError = errorMessage(err, 'Save failed — no version was created.')
    } finally {
      saving = false
    }
  }

  async function toggleVersions() {
    showVersions = !showVersions
    if (showVersions) {
      const targets = versions.filter(v => v.version > 1).slice(0, 8)
      await Promise.all(targets.map(async v => {
        if (diffs[v.version]) return
        try {
          diffs[v.version] = await api.diffVersions(configId, v.version - 1, v.version)
        } catch { /* diff is decorative; ignore */ }
      }))
    }
  }

  async function rollback(v: number) {
    const ok = await dialog.confirm({
      title: `Roll back to v${v}?`,
      body: `A new version identical to v${v} is committed on top — nothing is rewritten.`,
      confirmLabel: `Roll back`,
    })
    if (!ok) return
    try {
      await api.rollback(configId, v, `Rollback to v${v} from the Atrium editor`)
      await load(configId)
    } catch (err) {
      flashToast(errorMessage(err, 'Rollback failed.'))
    }
  }

  async function toggleLock(row: Row) {
    try {
      if (lockedKeys.has(row.key)) {
        await api.unlockKey(configId, row.key)
        lockedKeys.delete(row.key)
      } else {
        await api.lockKey(configId, row.key)
        lockedKeys.add(row.key)
      }
      lockedKeys = new Set(lockedKeys)
    } catch (err) {
      flashToast(errorMessage(err, 'Lock change failed.'))
    }
  }

  // ── advisory max-age policy ──────────────────────────────────────────────
  function openKeyMaxAge(row: Row) {
    maxAgeKeyFor = maxAgeKeyFor === row.key ? null : row.key
    // Prefill only when this key has its OWN override (not the inherited default).
    maxAgeDraft = overriddenKeys.has(row.key) && row.maxAgeSeconds != null
      ? humanizeDuration(row.maxAgeSeconds) : ''
  }

  async function saveKeyMaxAge(row: Row) {
    const secs = parseDurationToSeconds(maxAgeDraft)
    if (secs === null) { flashToast('Enter a duration like 90d, 24h, or 2160h.'); return }
    try {
      await api.setKeyMaxAge(configId, row.key, secs)
      maxAgeKeyFor = null
      await load(configId)
    } catch (err) {
      flashToast(errorMessage(err, 'Could not set max-age.'))
    }
  }

  async function clearKeyMaxAge(row: Row) {
    try {
      await api.setKeyMaxAge(configId, row.key, null)
      maxAgeKeyFor = null
      await load(configId)
    } catch (err) {
      flashToast(errorMessage(err, 'Could not clear max-age.'))
    }
  }

  function openConfigMaxAge() {
    showConfigMaxAge = !showConfigMaxAge
    configMaxAgeDraft = configDefaultMaxAge != null ? humanizeDuration(configDefaultMaxAge) : ''
  }

  async function saveConfigMaxAge() {
    const secs = parseDurationToSeconds(configMaxAgeDraft)
    if (secs === null) { flashToast('Enter a duration like 90d, 24h, or 2160h.'); return }
    try {
      await api.setConfigMaxAge(configId, secs)
      showConfigMaxAge = false
      await load(configId)
    } catch (err) {
      flashToast(errorMessage(err, 'Could not set config max-age.'))
    }
  }

  async function clearConfigMaxAge() {
    try {
      await api.setConfigMaxAge(configId, null)
      showConfigMaxAge = false
      await load(configId)
    } catch (err) {
      flashToast(errorMessage(err, 'Could not clear config max-age.'))
    }
  }

  const staleCount = $derived(rows.filter(r => r.stale && !r.added).length)

  // ── per-key annotation (owner + note) ────────────────────────────────────
  function openKeyAnnotation(row: Row) {
    if (annotationKeyFor === row.key) { annotationKeyFor = null; return }
    annotationKeyFor = row.key
    annotationOwnerDraft = row.owner ?? ''
    annotationNoteDraft = row.note ?? ''
  }

  async function saveKeyAnnotation(row: Row) {
    const owner = annotationOwnerDraft.trim()
    const note = annotationNoteDraft.trim()
    try {
      await api.setKeyAnnotation(configId, row.key, owner || null, note || null)
      annotationKeyFor = null
      await load(configId)
    } catch (err) {
      flashToast(errorMessage(err, 'Could not save annotation.'))
    }
  }

  async function clearKeyAnnotation(row: Row) {
    try {
      // Empty owner + note clears the whole annotation server-side.
      await api.setKeyAnnotation(configId, row.key, null, null)
      annotationKeyFor = null
      await load(configId)
    } catch (err) {
      flashToast(errorMessage(err, 'Could not clear annotation.'))
    }
  }

  async function toggleHistory(row: Row) {
    if (historyFor === row.key) {
      historyFor = null
      return
    }
    historyFor = row.key
    keyHistory = []
    historicValues = {}
    try {
      const res = await api.keyHistory(configId, row.key)
      keyHistory = res.history
    } catch (err) {
      flashToast(errorMessage(err, 'Could not load key history.'))
      historyFor = null
    }
  }

  async function revealHistoric(key: string, version: number) {
    try {
      const res = await api.revealKeyVersion(configId, key, version)
      historicValues[version] = res.value
      flashToast(`Reveal of ${key} v${version} recorded in the audit ledger`)
    } catch (err) {
      flashToast(errorMessage(err, 'Reveal failed.'))
    }
  }

  async function deleteConfig() {
    const label = ctx ? `${ctx.env.slug}/${ctx.config.name}` : 'this config'
    const ok = await dialog.confirm({
      title: `Move ${label} to the trash?`,
      body: 'Restorable from Trash until destroyed.',
      confirmLabel: 'Move to trash',
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteConfig(configId)
      await registry.hydrate(true)
      router.go(ctx ? `/projects/${ctx.project.id}` : '/projects')
    } catch (err) {
      flashToast(errorMessage(err, 'Delete failed.'))
    }
  }

  /* ── download .env ──────────────────────────── */

  function envQuote(v: string): string {
    if (v === '') return '""'
    if (/[\n\r"'#\s\\$]/.test(v)) {
      return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n').replace(/\r/g, '\\r') + '"'
    }
    return v
  }

  async function downloadEnv() {
    if (!ctx) return
    const exportable = rows.filter(r => !r.deleted && !r.added && r.key)
    const ok = await dialog.confirm({
      title: 'Download .env with plaintext values?',
      body: `All ${exportable.length} values are revealed (each recorded in the audit ledger) and written to a plaintext file on this machine.`,
      confirmLabel: 'Reveal & download',
      danger: true,
    })
    if (!ok) return
    try {
      const revealed = await Promise.all(
        exportable.map(async r => {
          if (r.revealed && r.value !== null) return { key: r.key, value: r.value }
          const res = await api.revealKey(configId, r.key)
          return { key: r.key, value: res.value }
        }),
      )
      const lines: string[] = [
        `# ${ctx.project.name} / ${ctx.env.slug} / ${ctx.config.name} — exported from Janus (v${latestVersion})`,
      ]
      for (const { key, value } of revealed.sort((a, b) => a.key.localeCompare(b.key))) {
        if (isEnvVarKey(key)) lines.push(`${key}=${envQuote(value)}`)
        else lines.push(`# skipped (not an env-var name — use janus secrets download --format files): ${key}`)
      }
      const blob = new Blob([lines.join('\n') + '\n'], { type: 'text/plain' })
      const url = URL.createObjectURL(blob)
      try {
        const a = document.createElement('a')
        a.href = url
        a.download = `${ctx.project.name}-${ctx.env.slug}-${ctx.config.name}.env`
        document.body.appendChild(a)
        a.click()
        a.remove()
      } finally {
        URL.revokeObjectURL(url)
      }
      flashToast(`Downloaded ${revealed.length} values — every reveal recorded in the audit ledger`)
    } catch (err) {
      flashToast(errorMessage(err, 'Export failed.'))
    }
  }

  /* ── bulk import (.env / .properties) ─────── */

  const importEntries = $derived.by((): ImportedEntry[] => (importText.trim() ? parseEnvOrProps(importText) : []))

  $effect(() => {
    // default selection: everything parseable
    const sel: Record<number, boolean> = {}
    for (const e of importEntries) sel[e.line] = !e.error
    importPicked = sel
  })

  function importStatus(e: ImportedEntry): 'new' | 'overwrite' | 'invalid' {
    if (e.error) return 'invalid'
    return rows.some(r => !r.added && r.key === e.key) ? 'overwrite' : 'new'
  }

  async function onImportFile(ev: Event) {
    const file = (ev.currentTarget as HTMLInputElement).files?.[0]
    if (!file) return
    importText = await file.text()
    ;(ev.currentTarget as HTMLInputElement).value = ''
  }

  function applyImport() {
    let added = 0
    let updated = 0
    const seen = new Set<string>()
    for (const e of importEntries) {
      if (e.error || !importPicked[e.line] || seen.has(e.key)) continue
      seen.add(e.key)
      const existing = rows.find(r => r.key === e.key)
      if (existing) {
        existing.draft = e.value
        existing.revealed = true
        existing.dirty = true
        existing.deleted = false
        updated++
      } else {
        rows.push({
          key: e.key, origin: 'own', valueVersion: 0, createdAt: new Date().toISOString(),
          maxAgeSeconds: null, stale: false, lastReadAt: null, unused: false, owner: null, note: null,
          revealed: true, value: null, draft: e.value, dirty: true, deleted: false, added: true, editing: false,
        })
        added++
      }
    }
    showImport = false
    importText = ''
    flashToast(`Imported ${added + updated} key${added + updated === 1 ? '' : 's'} into the draft (${added} new, ${updated} overwriting) — review, then save`)
  }

  const mask = '•'.repeat(14)
</script>

{#if !ctx}
  {#if registry.loading || loading}
    <p class="folio">Opening the config…</p>
  {:else}
    <NotFound />
  {/if}
{:else}
  {@const { project, env, config } = ctx}
  <div class="editor">
    <header class="page-head rise">
      <div>
        <p class="folio">
          <a href="/projects">Registry</a> / <a href={`/projects/${project.id}`}>{project.name}</a> / {env.slug}
        </p>
        <div class="title-line">
          <h1 class="mono-title">{config.name}</h1>
          <span class="pill pill-{env.kind}">{env.slug}</span>
          <span class="ver-chip mono">FOL. v{latestVersion}</span>
        </div>
        {#if config.inheritsFrom}
          <p class="folio inherit-note">⤷ inherits from <strong>{env.configs.find(c => c.id === config.inheritsFrom)?.name ?? 'base'}</strong> — child wins per key</p>
        {/if}
      </div>
      <div class="head-actions">
        <button class="btn btn-sm" onclick={toggleVersions}>
          {showVersions ? 'Hide history' : `History (${versions.length})`}
        </button>
        <button class="btn btn-sm" onclick={() => (showPromote = !showPromote)}>
          {showPromote ? 'Close promote' : 'Promote →'}
        </button>
        <button class="btn btn-sm" onclick={revealAll} disabled={!anyHidden}>Reveal all</button>
        <button class="btn btn-sm btn-ghost del-btn" onclick={deleteConfig}>Delete</button>
      </div>
    </header>
    <hr class="ledger-rule" />

    {#if loadError}
      <p class="error rise">{loadError}</p>
    {/if}

    {#if toast}
      <p class="reveal-note sheet" role="status">
        <svg width="11" height="11" viewBox="0 0 12 12" fill="none" aria-hidden="true"><path d="M2.5 6.5 L5 9 L9.5 3.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>
        {toast}
      </p>
    {/if}

    {#if showPromote}
      <PromotePanel {project} {env} {config} onDone={(msg) => { flashToast(msg); showPromote = false }} />
    {/if}

    {#if showVersions}
      <section class="versions sheet rise">
        <div class="section-head">
          <h3>Config versions</h3>
          <span class="folio">each save is one immutable version — diff &amp; rollback</span>
        </div>
        <table class="ledger">
          <thead>
            <tr><th>Fol.</th><th>Saved</th><th>By</th><th>Changes</th><th></th></tr>
          </thead>
          <tbody>
            {#each versions as v (v.version)}
              <tr>
                <td class="num ver-cell">v{v.version}{#if v.version === latestVersion}<span class="pill pill-info current-pill">current</span>{/if}</td>
                <td class="num">{stampDate(v.created_at)} · {relTime(v.created_at)}</td>
                <td>{userEmails.get(v.created_by) ?? (v.created_by.includes('-') ? `${v.created_by.slice(0, 8)}…` : v.created_by)}</td>
                <td class="changes">
                  {#if diffs[v.version]}
                    {#each diffs[v.version].added as k}<span class="chg add mono">+ {k}</span>{/each}
                    {#each diffs[v.version].changed as k}<span class="chg mod mono">~ {k}</span>{/each}
                    {#each diffs[v.version].removed as k}<span class="chg del mono">− {k}</span>{/each}
                  {:else if v.message}
                    <span class="folio">{v.message}</span>
                  {/if}
                </td>
                <td class="row-actions">
                  {#if v.version !== latestVersion}<button class="btn btn-ghost btn-sm" onclick={() => rollback(v.version)}>Roll back</button>{/if}
                </td>
              </tr>
            {:else}
              <tr><td colspan="5" class="folio">No versions yet — the first save creates v1.</td></tr>
            {/each}
          </tbody>
        </table>
      </section>
    {/if}

    <div class="toolbar rise" style="animation-delay: 60ms">
      <input class="input search" placeholder={`Filter ${rows.length} keys…`} bind:value={filter} />
      <div class="toolbar-actions">
        {#if staleCount > 0}
          <span class="stamp flat maxage-chip" title="Keys past their advisory max-age (never blocks anything)">{staleCount} past max-age</span>
        {/if}
        <button class="btn" class:has-override={configDefaultMaxAge != null} onclick={openConfigMaxAge}>
          {showConfigMaxAge ? 'Close max-age' : configDefaultMaxAge != null ? `Max-age · ${humanizeDuration(configDefaultMaxAge)}` : 'Max-age…'}
        </button>
        <button class="btn" onclick={() => (showImport = !showImport)}>{showImport ? 'Close import' : 'Import…'}</button>
        <button class="btn" onclick={downloadEnv} disabled={!rows.some(r => !r.added)}>Download .env</button>
        <button class="btn" onclick={addRow}>+ Add secret</button>
      </div>
    </div>

    {#if showConfigMaxAge}
      <section class="sheet maxage-config-panel rise">
        <div class="section-head">
          <h3>Config default max-age</h3>
          <span class="folio">advisory expiry — flags stale keys, never blocks any operation</span>
        </div>
        <p class="folio">
          Applies to every key without its own override. A key is flagged
          <span class="stamp flat maxage-chip">past max-age</span> once its current value is older than this.
        </p>
        <div class="maxage-controls">
          <input class="input mono maxage-input" placeholder="e.g. 90d, 24h, 2160h"
            bind:value={configMaxAgeDraft}
            onkeydown={(e) => { if (e.key === 'Enter') saveConfigMaxAge() }} />
          <button class="btn btn-primary btn-sm" onclick={saveConfigMaxAge}>Set default</button>
          {#if configDefaultMaxAge != null}
            <button class="btn btn-ghost btn-sm" onclick={clearConfigMaxAge}>Clear default</button>
          {/if}
        </div>
      </section>
    {/if}

    {#if showImport}
      <section class="sheet import-panel rise">
        <div class="section-head">
          <h3>Bulk import</h3>
          <span class="folio">.env or Java .properties — parsed locally, staged into the draft, committed on Save</span>
        </div>
        <div class="import-input">
          <textarea
            class="input mono"
            rows="6"
            spellcheck="false"
            bind:value={importText}
            placeholder={'# paste here, or choose a file\nDATABASE_URL=postgres://…\nexport API_TOKEN="tok_…"\napp.timeout: 30s'}
          ></textarea>
          <label class="btn btn-sm file-btn">
            Choose file…
            <input type="file" accept=".env,.properties,.txt,text/plain" onchange={onImportFile} hidden />
          </label>
        </div>

        {#if importEntries.length}
          <table class="ledger import-preview">
            <thead>
              <tr><th style="width: 36px"></th><th>Key</th><th>Value</th><th style="width: 120px">Action</th></tr>
            </thead>
            <tbody>
              {#each importEntries as e (e.line)}
                {@const st = importStatus(e)}
                <tr class:invalid={st === 'invalid'}>
                  <td>
                    <input type="checkbox" checked={importPicked[e.line] ?? false} disabled={st === 'invalid'}
                      onchange={(ev) => (importPicked[e.line] = (ev.currentTarget as HTMLInputElement).checked)} />
                  </td>
                  <td class="mono key">
                    {e.key || '—'}
                    {#if !e.error && !isEnvVarKey(e.key)}<span class="file-badge">file</span>{/if}
                  </td>
                  <td class="mono val-preview">{e.error ? '' : e.value.split('\n')[0].slice(0, 48) + (e.value.length > 48 || e.value.includes('\n') ? '…' : '')}</td>
                  <td>
                    {#if st === 'invalid'}<span class="state bad" title={e.error}>line {e.line}: {e.error}</span>
                    {:else if st === 'overwrite'}<span class="chg mod mono">~ overwrite</span>
                    {:else}<span class="chg add mono">+ new</span>{/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
          <div class="import-foot">
            <span class="folio">
              {importEntries.filter(e => !e.error && importPicked[e.line]).length} selected ·
              {importEntries.filter(e => e.error).length} invalid skipped
            </span>
            <button class="btn btn-stamp" onclick={applyImport}
              disabled={!importEntries.some(e => !e.error && importPicked[e.line])}>
              Stage into draft
            </button>
          </div>
        {/if}
      </section>
    {/if}

    {#if selCount > 0}
      <div class="bulkbar plate rise" role="region" aria-label="Bulk actions">
        <span class="bulk-count"><strong>{selCount}</strong> selected</span>
        <div class="bulk-actions">
          <button class="btn btn-sm" onclick={bulkReveal}>Reveal selected</button>
          <button class="btn btn-sm" onclick={bulkExportEnv}>Export selected .env</button>
          <button class="btn btn-sm btn-ghost del-btn" onclick={bulkDelete}>Delete selected</button>
          <button class="btn btn-sm btn-ghost" onclick={clearSelection}>Clear</button>
        </div>
      </div>
    {/if}

    <div class="sheet table-wrap rise" style="animation-delay: 100ms">
      <table class="ledger">
        <thead>
          <tr>
            <th style="width: 30px" class="sel-cell">
              <input type="checkbox" aria-label="Select all visible keys"
                checked={allVisibleSelected}
                indeterminate={selCount > 0 && !allVisibleSelected}
                disabled={selectableVisible.length === 0}
                onchange={toggleSelectAll} />
            </th>
            <th style="width: 26%">Key</th>
            <th>Value</th>
            <th style="width: 100px">Origin</th>
            <th style="width: 60px">Ver.</th>
            <th style="width: 140px">Amended</th>
            <th style="width: 130px"></th>
          </tr>
        </thead>
        <tbody>
          {#each visible as row (row)}
            <tr class:dirty={row.dirty || row.added} class:deleted={row.deleted} class:selected={!row.added && selected.has(row.key)}>
              <td class="sel-cell">
                {#if !row.added && row.key}
                  <input type="checkbox" aria-label={`Select ${row.key}`}
                    checked={selected.has(row.key)}
                    onchange={() => toggleRowSelect(row.key)} />
                {/if}
              </td>
              <td class="key-cell">
                {#if row.added && row.editing}
                  <input class="input inline-input mono" placeholder="NEW_KEY or config.yaml" bind:value={row.key} />
                  {#if row.key.trim() && !isValidKey(row.key.trim())}
                    <span class="key-err">letters, digits, and . _ - only — no slashes, not "." or ".."</span>
                  {:else if row.key.trim() && !isEnvVarKey(row.key.trim())}
                    <span class="file-hint" title="Not an env-var identifier — janus run skips it; use janus secrets download --format files to materialize it to disk">file key — skipped by janus run</span>
                  {/if}
                {:else}
                  <span class="mono key">{row.key}</span>
                  {#if lockedKeys.has(row.key)}
                    <span class="lock-mark" title="Locked — promotions cannot overwrite this key">⚿</span>
                  {/if}
                  {#if row.stale && row.maxAgeSeconds != null}
                    <span class="stamp flat maxage-chip" title={`Advisory: past its max-age of ${humanizeDuration(row.maxAgeSeconds)} (age ${relTime(row.createdAt)}). Nothing is blocked.`}>past max-age</span>
                  {/if}
                  {#if row.unused && !row.added}
                    {#if row.lastReadAt}
                      <span class="stamp flat unused-chip" title={`Advisory: not read since ${row.lastReadAt} (${relTime(row.lastReadAt)}), past the unused threshold. Nothing is blocked.`}>not read 90d+</span>
                    {:else}
                      <span class="stamp flat unused-chip" title="Advisory: no per-key reveal on record. Nothing is blocked. (Bulk raw reads are not per-key attributable.)">never read</span>
                    {/if}
                  {/if}
                  {#if row.key && !isEnvVarKey(row.key)}
                    <span class="file-badge" title="Not an env-var identifier — janus run skips it; janus secrets download --format files materializes it to disk">file</span>
                  {/if}
                  {#if (row.owner || row.note) && !row.added}
                    <div class="annotation-line" title="Annotation — owner and note (metadata, never a value)">
                      {#if row.owner}<span class="owner-chip">👤 {row.owner}</span>{/if}
                      {#if row.note}<span class="note-text folio">{row.note}</span>{/if}
                    </div>
                  {/if}
                {/if}
              </td>
              <td class="val-cell">
                {#if row.editing}
                  {@const fc = checkFormat(row.draft, row.type)}
                  <!-- textarea: values may be JSON, PEM, whole files. Enter inserts a
                       newline; Ctrl/Cmd+Enter or blur commits into the dirty buffer. -->
                  <div class="val-editrow">
                    <textarea
                      class="input inline-input val-edit mono"
                      rows={Math.min(Math.max(row.draft.split('\n').length, 1), 14)}
                      spellcheck="false"
                      autocomplete="off"
                      bind:value={row.draft}
                      onblur={() => commitEdit(row)}
                      onkeydown={(e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); commitEdit(row) } }}
                      placeholder="value — paste JSON, PEM, or any file content; Ctrl+Enter to apply"
                    ></textarea>
                    <GenerateMenu onGenerate={(value) => applyGenerated(row, value)} />
                  </div>
                  <!-- JSON/PEM awareness: sniffed client-side from the draft (or the
                       declared type); advisory only — an invalid value still saves. -->
                  {#if fc}
                    <div class="fmt-row">
                      <span class="fmt-badge mono" class:bad={!fc.ok}>
                        {fc.format === 'json' ? 'JSON' : `PEM · ${fc.label ?? '?'}`}{#if fc.extraBlocks} +{fc.extraBlocks}{/if}
                      </span>
                      {#if fc.ok}
                        <span class="fmt-ok">✓ well-formed</span>
                        {#if fc.format === 'json' && prettyJson(row.draft) !== row.draft}
                          <button class="btn btn-ghost btn-sm" onclick={() => { row.draft = prettyJson(row.draft) ?? row.draft; markDirty(row) }}>
                            Pretty-print
                          </button>
                        {/if}
                      {:else}
                        <span class="fmt-err" title="Advisory only — saving is not blocked">{fc.error}</span>
                      {/if}
                    </div>
                  {/if}
                {:else if row.revealed}
                  <button class="val revealed mono" onclick={() => beginEdit(row)} title="Click to edit">
                    {(row.draft.split('\n')[0] || '(empty)')}{#if row.draft.includes('\n')}<span class="more-lines"> ⏎ {row.draft.split('\n').length} lines</span>{/if}
                  </button>
                {:else}
                  <button class="val mono masked" onclick={() => reveal(row)} title="Reveal (recorded in audit ledger)">{mask}</button>
                {/if}
              </td>
              <td>
                {#if row.origin === 'inherited'}<span class="pill pill-info">inherited</span>
                {:else if row.origin === 'overridden'}<span class="pill pill-neutral">override</span>
                {:else}<span class="folio">own</span>{/if}
              </td>
              <td class="num">{row.valueVersion ? `v${row.valueVersion}` : '—'}</td>
              <td class="folio amended">{relTime(row.createdAt)}</td>
              <td class="row-actions">
                {#if row.revealed && !row.editing && !row.added}
                  <button class="btn btn-ghost btn-sm" onclick={() => { row.revealed = false; row.editing = false }} title="Mask">Mask</button>
                {/if}
                {#if !row.added}
                  <button class="btn btn-ghost btn-sm" onclick={() => toggleInsights(row.key)}
                    title="Read insights — last-read time and a 30-day daily reveal sparkline (audit metadata; no value)">
                    {insightsFor === row.key ? 'Close reads' : 'Reads…'}
                  </button>
                  <button class="btn btn-ghost btn-sm" onclick={() => toggleHistory(row)} title="Per-key value history">
                    {historyFor === row.key ? 'Close' : `v${row.valueVersion}…`}
                  </button>
                  <button class="btn btn-ghost btn-sm" onclick={() => toggleLock(row)}
                    title={lockedKeys.has(row.key) ? 'Unlock for promotion' : 'Lock against promotion'}>
                    {lockedKeys.has(row.key) ? 'Unlock' : 'Lock'}
                  </button>
                  <button class="btn btn-ghost btn-sm" class:has-override={overriddenKeys.has(row.key)}
                    onclick={() => openKeyMaxAge(row)}
                    title="Advisory max-age override for this key">
                    Max-age{#if overriddenKeys.has(row.key)} ·&nbsp;set{/if}
                  </button>
                  <button class="btn btn-ghost btn-sm" class:has-override={!!(row.owner || row.note)}
                    onclick={() => openKeyAnnotation(row)}
                    title="Owner + note annotation for this key (metadata, never a value)">
                    {annotationKeyFor === row.key ? 'Close owner' : (row.owner || row.note) ? 'Owner · set' : 'Owner…'}
                  </button>
                {/if}
                {#if row.origin !== 'inherited' || row.added}
                  <button class="btn btn-ghost btn-sm del-btn" onclick={() => toggleDelete(row)}>
                    {row.deleted ? 'Restore' : 'Delete'}
                  </button>
                {/if}
              </td>
            </tr>
            {#if insightsFor === row.key}
              {@const ins = insights[row.key]}
              <tr class="insights-row">
                <td colspan="7">
                  <div class="insights">
                    <span class="label">Read insights — {row.key}</span>
                    <p class="folio">
                      From the audit ledger — key names, reveal counts, and timestamps only; never a value.
                    </p>
                    <div class="insights-body">
                      <div class="insight-stat">
                        <span class="insight-label">Last read</span>
                        {#if ins?.last_read_at}
                          <span class="insight-val mono" title={ins.last_read_at}>{relTime(ins.last_read_at)}</span>
                        {:else}
                          <span class="insight-val never">never read per-key</span>
                        {/if}
                      </div>
                      <div class="insight-stat">
                        <span class="insight-label">Reveals · last {insightsWindow}d</span>
                        {#if ins && ins.daily.some(n => n > 0)}
                          <span class="spark"><Sparkline data={ins.daily} width={160} height={30} color="var(--archivist)" /></span>
                          <span class="insight-val mono">{ins.daily.reduce((a, b) => a + b, 0)} total</span>
                        {:else}
                          <span class="insight-val never">no reveals in {insightsWindow}d</span>
                        {/if}
                      </div>
                    </div>
                  </div>
                </td>
              </tr>
            {/if}
            {#if maxAgeKeyFor === row.key}
              <tr class="maxage-row">
                <td colspan="7">
                  <div class="maxage-panel">
                    <span class="label">Advisory max-age — {row.key}</span>
                    <p class="folio">
                      Flags this key as past max-age once its current value is older than the duration.
                      Advisory only — reads, writes, and reveals are never blocked.
                      {#if configDefaultMaxAge != null && !overriddenKeys.has(row.key)}
                        Inherits the config default of {humanizeDuration(configDefaultMaxAge)}.
                      {/if}
                    </p>
                    <div class="maxage-controls">
                      <input class="input mono maxage-input" placeholder="e.g. 90d, 24h, 2160h"
                        bind:value={maxAgeDraft}
                        onkeydown={(e) => { if (e.key === 'Enter') saveKeyMaxAge(row) }} />
                      <button class="btn btn-primary btn-sm" onclick={() => saveKeyMaxAge(row)}>Set override</button>
                      {#if overriddenKeys.has(row.key)}
                        <button class="btn btn-ghost btn-sm" onclick={() => clearKeyMaxAge(row)}>Clear override</button>
                      {/if}
                    </div>
                  </div>
                </td>
              </tr>
            {/if}
            {#if annotationKeyFor === row.key}
              <tr class="annotation-row">
                <td colspan="7">
                  <div class="annotation-panel">
                    <span class="label">Annotation — {row.key}</span>
                    <p class="folio">
                      Owner and free-text note so “what is this and who do I ask” is answerable.
                      Metadata only — no secret value is stored. Never blocks any operation.
                    </p>
                    <div class="annotation-controls">
                      <input class="input annotation-owner" placeholder="owner — e.g. team-data, alice@corp.io"
                        maxlength="256"
                        bind:value={annotationOwnerDraft}
                        onkeydown={(e) => { if (e.key === 'Enter') saveKeyAnnotation(row) }} />
                      <textarea class="input annotation-note" placeholder="note — what is this secret, when to rotate, who owns it…"
                        rows="2" maxlength="2048"
                        bind:value={annotationNoteDraft}></textarea>
                      <div class="annotation-actions">
                        <button class="btn btn-primary btn-sm" onclick={() => saveKeyAnnotation(row)}>Save</button>
                        {#if row.owner || row.note}
                          <button class="btn btn-ghost btn-sm" onclick={() => clearKeyAnnotation(row)}>Clear</button>
                        {/if}
                      </div>
                    </div>
                  </div>
                </td>
              </tr>
            {/if}
            {#if historyFor === row.key}
              <tr class="hist-row">
                <td colspan="7">
                  {#if !keyHistory.length}
                    <span class="folio">Loading value history…</span>
                  {:else}
                    <div class="hist">
                      <span class="label">Value history — {row.key}</span>
                      {#each keyHistory as h (h.value_version)}
                        <div class="hist-line">
                          <span class="mono hv">v{h.value_version}</span>
                          <span class="folio">{stampDate(h.created_at)} · {relTime(h.created_at)}</span>
                          {#if historicValues[h.value_version] !== undefined}
                            <code class="mono hist-val">{historicValues[h.value_version]}</code>
                          {:else}
                            <button class="btn btn-ghost btn-sm" onclick={() => revealHistoric(row.key, h.value_version)}>
                              Reveal (audited)
                            </button>
                          {/if}
                        </div>
                      {/each}
                    </div>
                  {/if}
                </td>
              </tr>
            {/if}
          {:else}
            <tr><td colspan="7" class="empty folio">
              {loading ? 'Unwrapping…' : rows.length ? `No keys match “${filter}”.` : 'No secrets yet — add the first key.'}
            </td></tr>
          {/each}
        </tbody>
      </table>
    </div>

    <p class="foot-note folio">
      Masked list reads metadata only. Revealing a value is a read and is recorded in the
      <a href="/audit">audit ledger</a>. Deletes are soft — recoverable until destroyed.
    </p>

    {#if dirtyCount > 0}
      <div class="savebar plate">
        <span class="save-count">
          <strong>{dirtyCount}</strong> uncommitted amendment{dirtyCount === 1 ? '' : 's'}
        </span>
        <span class="folio">will be committed together as one immutable version</span>
        {#if badKeys > 0}<span class="error">{badKeys} key{badKeys === 1 ? ' is' : 's are'} not filename-safe</span>{/if}
        {#if saveError}<span class="error">{saveError}</span>{/if}
        <div class="save-actions">
          <button class="btn" onclick={discard} disabled={saving}>Discard</button>
          <button class="btn btn-stamp" onclick={saveAll} disabled={saving || badKeys > 0}>
            {saving ? 'Committing…' : `Save as v${latestVersion + 1}`}
          </button>
        </div>
      </div>
    {/if}

    {#if savedFlash !== null}
      <div class="saved-stamp">
        <span class="stamp ok stamped">Committed — v{savedFlash}</span>
      </div>
    {/if}
  </div>
{/if}

<style>
  .editor { max-width: 1200px; margin: 0 auto; padding-bottom: var(--s8); }

  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .title-line { display: flex; align-items: center; gap: var(--s3); margin-top: var(--s1); }
  .mono-title { font-family: var(--font-mono); font-weight: 600; font-size: var(--text-xl); letter-spacing: -0.02em; }
  .ver-chip {
    font-size: var(--text-xs);
    color: var(--vermilion);
    border: 1.5px solid currentColor;
    border-radius: 2px;
    padding: 0.1rem 0.45rem;
    letter-spacing: 0.12em;
    font-weight: 600;
  }
  .inherit-note { margin-top: var(--s2); color: var(--archivist); }
  .head-actions { display: flex; gap: var(--s2); }

  .error { color: var(--vermilion); font-size: var(--text-sm); }

  /* fixed toast — must not shift the ledger while the user is working */
  .reveal-note {
    position: fixed;
    left: calc(236px + var(--s6));
    bottom: var(--s5);
    z-index: 30;
    display: flex; align-items: center; gap: var(--s2);
    color: var(--verdigris);
    border-left: 3px solid var(--verdigris);
    padding: var(--s2) var(--s4);
    font-size: var(--text-xs);
    font-weight: 620;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    animation: rise-in var(--t-med) var(--ease-out) both;
  }

  .versions { margin-top: var(--s4); padding: var(--s4) var(--s5); }
  .versions .section-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: var(--s2); }
  .ver-cell { color: var(--vermilion); font-weight: 600; white-space: nowrap; }
  .current-pill { margin-left: var(--s2); }
  .changes { display: flex; flex-wrap: wrap; gap: 0.3rem; }
  .chg {
    font-size: var(--text-xs);
    padding: 0.06rem 0.4rem;
    border-radius: 2px;
    border: 1px solid;
  }
  .chg.add { color: var(--verdigris); background: var(--verdigris-wash); }
  .chg.mod { color: var(--archivist); background: var(--archivist-wash); }
  .chg.del { color: var(--vermilion); background: var(--vermilion-wash); text-decoration: line-through; }

  .toolbar { display: flex; justify-content: space-between; gap: var(--s3); margin: var(--s5) 0 var(--s3); }
  .toolbar-actions { display: flex; gap: var(--s2); }
  .search { max-width: 300px; }

  /* ── bulk import panel ──────────────────────── */
  .import-panel { padding: var(--s4) var(--s5); margin-bottom: var(--s3); border-left: 4px solid var(--verdigris); }
  .import-panel .section-head { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: var(--s3); flex-wrap: wrap; }
  .import-input { display: flex; flex-direction: column; gap: var(--s2); align-items: flex-start; }
  .import-input textarea { resize: vertical; white-space: pre; }
  .file-btn { position: relative; }
  .import-preview { margin-top: var(--s3); }
  .import-preview tr.invalid td { background: var(--vermilion-wash); opacity: 0.75; }
  .val-preview { font-size: var(--text-xs); color: var(--ink-soft); }
  .state.bad { color: var(--vermilion); font-size: var(--text-xs); font-weight: 650; text-transform: uppercase; letter-spacing: 0.06em; }
  .import-foot { display: flex; justify-content: space-between; align-items: center; margin-top: var(--s3); }

  .table-wrap { overflow-x: auto; }

  .key-cell .key { font-weight: 600; font-size: var(--text-sm); }

  .val {
    font-size: var(--text-sm);
    background: transparent;
    border: 0;
    cursor: pointer;
    padding: 0.15rem 0.3rem;
    border-radius: 2px;
    color: var(--ink);
    max-width: 420px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
    text-align: left;
  }
  .val.masked { color: var(--ink-ghost); letter-spacing: 2px; }
  .val.masked:hover { color: var(--archivist); background: var(--archivist-wash); }
  .val.revealed { background: var(--ochre-wash); }
  .val.revealed:hover { outline: 1px solid var(--rule-strong); }

  .inline-input { padding: 0.25rem 0.5rem; font-size: var(--text-sm); }
  .val-editrow { display: flex; align-items: flex-start; gap: var(--s2); }
  .val-editrow textarea { flex: 1; }
  .val-edit {
    resize: vertical;
    min-height: 1.9rem;
    max-height: 40vh;
    line-height: 1.45;
    white-space: pre;
    overflow-x: auto;
  }
  .more-lines { color: var(--archivist); font-size: var(--text-xs); }

  /* format hint under the value textarea — advisory, never blocks */
  .fmt-row { display: flex; align-items: center; gap: var(--s2); margin-top: var(--s1); }
  .fmt-badge {
    font-size: 0.58rem;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--archivist);
    border: 1px solid currentColor;
    border-radius: 2px;
    padding: 0.05rem 0.35rem;
  }
  .fmt-badge.bad { color: var(--ochre); }
  .fmt-ok { color: var(--verdigris); font-size: var(--text-xs); font-weight: 620; }
  .fmt-err { color: var(--ochre); font-size: var(--text-xs); }
  .amended { white-space: nowrap; }

  tr.dirty td { background: var(--ochre-wash); }
  tr.dirty td:first-child { box-shadow: inset 3px 0 0 var(--ochre); }
  tr.deleted .key, tr.deleted .val { text-decoration: line-through; opacity: 0.45; }
  tr.deleted td { background: var(--vermilion-wash); }
  tr.deleted td:first-child { box-shadow: inset 3px 0 0 var(--vermilion); }

  .row-actions { text-align: right; white-space: nowrap; }
  .del-btn:hover { color: var(--vermilion); }
  .empty { text-align: center; padding: var(--s6) !important; }

  .lock-mark { color: var(--vermilion); margin-left: 0.35rem; font-size: var(--text-sm); }

  /* advisory max-age (expiry) — vermilion "past max-age" chip + config controls */
  .maxage-chip {
    margin-left: 0.35rem;
    color: var(--vermilion);
    border-color: var(--vermilion);
    font-size: 0.6rem;
    letter-spacing: 0.04em;
  }
  /* advisory unused-secret detection — ochre "not read"/"never read" chip */
  .unused-chip {
    margin-left: 0.35rem;
    color: var(--ochre);
    border-color: var(--ochre);
    font-size: 0.6rem;
    letter-spacing: 0.04em;
  }
  .btn.has-override { color: var(--ochre); border-color: var(--ochre); }
  .maxage-row td { background: var(--archivist-wash); }
  .maxage-panel { padding: var(--s3) var(--s4); display: flex; flex-direction: column; gap: var(--s2); }
  .maxage-panel .folio, .maxage-config-panel .folio { max-width: 60ch; }
  .maxage-config-panel { padding: var(--s4) var(--s5); margin-top: var(--s3); border-left: 4px solid var(--vermilion); }
  .maxage-controls { display: flex; align-items: center; gap: var(--s2); flex-wrap: wrap; margin-top: var(--s2); }
  .maxage-input { width: 12rem; }

  /* per-key annotation (owner + note) — value-free metadata */
  .annotation-line { display: flex; align-items: baseline; gap: var(--s2); flex-wrap: wrap; margin-top: 0.25rem; }
  .owner-chip { font-size: var(--text-xs); color: var(--archivist); font-weight: 600; white-space: nowrap; }
  .note-text { font-size: var(--text-xs); color: var(--ink-soft); }
  .annotation-row td { background: var(--archivist-wash); }
  .annotation-panel { padding: var(--s3) var(--s4); display: flex; flex-direction: column; gap: var(--s2); }
  .annotation-panel .folio { max-width: 60ch; }
  .annotation-controls { display: flex; flex-direction: column; gap: var(--s2); margin-top: var(--s2); max-width: 44rem; }
  .annotation-owner { width: 100%; }
  .annotation-note { width: 100%; resize: vertical; font-family: inherit; }
  .annotation-actions { display: flex; align-items: center; gap: var(--s2); }
  .file-badge {
    margin-left: 0.4rem;
    font-size: 0.58rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--archivist);
    border: 1px solid currentColor;
    border-radius: 2px;
    padding: 0.02rem 0.3rem;
    vertical-align: middle;
  }
  .key-err {
    display: block;
    font-size: 0.65rem;
    color: var(--vermilion);
    margin-top: 2px;
  }
  .file-hint {
    display: block;
    font-size: 0.65rem;
    color: var(--archivist);
    margin-top: 2px;
  }
  /* ── bulk selection ─────────────────────────── */
  .sel-cell { text-align: center; width: 30px; }
  .sel-cell input { cursor: pointer; }
  tr.selected td { background: var(--archivist-wash); }
  tr.selected td:first-child { box-shadow: inset 3px 0 0 var(--archivist); }

  .bulkbar {
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: var(--s2) var(--s4);
    margin-bottom: var(--s3);
    border-left: 4px solid var(--archivist);
  }
  .bulk-count { font-size: var(--text-sm); }
  .bulk-actions { margin-left: auto; display: flex; gap: var(--s2); flex-wrap: wrap; }

  /* ── read insights panel ────────────────────── */
  .insights-row td { background: var(--paper-low); }
  .insights { display: flex; flex-direction: column; gap: var(--s2); padding: var(--s3) var(--s4); }
  .insights .folio { max-width: 60ch; }
  .insights-body { display: flex; gap: var(--s6); flex-wrap: wrap; align-items: center; margin-top: var(--s1); }
  .insight-stat { display: flex; align-items: center; gap: var(--s2); }
  .insight-label {
    font-size: 0.6rem;
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--archivist);
  }
  .insight-val { font-size: var(--text-sm); }
  .insight-val.never { color: var(--ink-ghost); font-style: italic; font-size: var(--text-xs); }
  .spark { display: inline-flex; align-items: center; }

  .hist-row td { background: var(--archivist-wash); }
  .hist { display: flex; flex-direction: column; gap: var(--s2); padding: var(--s2) var(--s3); }
  .hist-line { display: flex; align-items: center; gap: var(--s4); }
  .hv { font-weight: 600; color: var(--archivist); min-width: 2.4rem; }
  .hist-val {
    background: var(--paper-low);
    border: 1px dashed var(--rule-strong);
    border-radius: 2px;
    padding: 0.1rem 0.5rem;
    font-size: var(--text-xs);
    word-break: break-all;
  }

  .foot-note { margin-top: var(--s3); }

  /* ── save bar ───────────────────────────────── */
  .savebar {
    position: fixed;
    bottom: var(--s5);
    left: calc(236px + var(--s6));
    right: var(--s6);
    max-width: 1200px;
    margin: 0 auto;
    display: flex;
    align-items: center;
    gap: var(--s3);
    padding: var(--s3) var(--s5);
    animation: rise-in var(--t-med) var(--ease-out) both;
    z-index: 20;
    border-left: 4px solid var(--ochre);
  }
  .save-count { font-size: var(--text-sm); }
  .save-actions { margin-left: auto; display: flex; gap: var(--s3); }

  .saved-stamp {
    position: fixed;
    bottom: var(--s6);
    right: var(--s7);
    z-index: 30;
  }
  .stamped { animation: stamp-down 450ms var(--ease-press) both; font-size: var(--text-sm); }
</style>
