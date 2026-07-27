<script lang="ts">
  import {
    api, memberScopePath, groupScopePath, errorMessage,
    type UserInfo, type ApiMember, type Role,
    type ApiGroup, type ApiGroupBinding, type GroupRole, type ApiDerivedMember,
  } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

  type ScopeKind = 'instance' | 'project' | 'environment'

  let scopeKind = $state<ScopeKind>('instance')
  let pid = $state('')
  let eid = $state('')

  let users = $state<UserInfo[]>([])
  let members = $state<ApiMember[]>([])
  let loading = $state(true)
  let error = $state('')

  let groups = $state<ApiGroup[]>([])
  let groupBindings = $state<ApiGroupBinding[]>([])
  let derivedMembers = $state<ApiDerivedMember[]>([])
  let derivedTruncated = $state(false)
  let groupError = $state('')
  let bindGroupId = $state('')
  let bindRole = $state<GroupRole>('viewer')

  let inviting = $state(false)
  let inviteEmail = $state('')
  let invited = $state<{ email: string; password: string } | null>(null)
  let inviteError = $state('')

  const roleRank: Record<Role, number> = { viewer: 0, developer: 1, admin: 2, owner: 3 }

  const scopePath = $derived.by(() => {
    if (scopeKind === 'instance') return memberScopePath({ kind: 'instance' })
    if (scopeKind === 'project') return pid ? memberScopePath({ kind: 'project', pid }) : null
    return pid && eid ? memberScopePath({ kind: 'environment', pid, eid }) : null
  })

  const groupPath = $derived.by(() => {
    if (scopeKind === 'instance') return groupScopePath({ kind: 'instance' })
    if (scopeKind === 'project') return pid ? groupScopePath({ kind: 'project', pid }) : null
    return pid && eid ? groupScopePath({ kind: 'environment', pid, eid }) : null
  })

  const envOptions = $derived(registry.findProject(pid)?.environments ?? [])

  $effect(() => {
    if (!pid && registry.projects.length) pid = registry.projects[0].id
  })
  $effect(() => {
    if (scopeKind === 'environment' && !envOptions.some(e => e.id === eid)) eid = envOptions[0]?.id ?? ''
  })

  $effect(() => {
    api.listUsers().then(us => (users = us)).catch(() => (users = []))
    // The catalog needs group:manage, which a scope admin may not hold — an
    // empty list just means no picker, never a broken screen.
    api.listGroups().then(gs => (groups = gs)).catch(() => (groups = []))
  })

  $effect(() => {
    if (groupPath) void loadGroupBindings(groupPath)
    else groupBindings = []
  })

  $effect(() => {
    if (scopePath) void load(scopePath)
    else members = []
  })

  async function load(path: string) {
    loading = true
    error = ''
    try {
      members = await api.listScopedMembers(path)
    } catch (err) {
      error = errorMessage(err, 'Could not list members for this scope.')
      members = []
    } finally {
      loading = false
    }
  }

  async function loadGroupBindings(path: string) {
    groupError = ''
    try {
      const res = await api.scopedGroupAccess(path)
      groupBindings = res.bindings
      derivedMembers = res.derived
      derivedTruncated = res.truncated
    } catch {
      // member:read covers both lists, so a failure here is a transport
      // problem rather than a permission one; keep the screen usable.
      groupBindings = []
      derivedMembers = []
      derivedTruncated = false
    }
  }

  async function bindGroup() {
    if (!groupPath || !bindGroupId) return
    groupError = ''
    try {
      await api.putScopedGroupBinding(groupPath, bindGroupId, bindRole)
      bindGroupId = ''
      await loadGroupBindings(groupPath)
    } catch (err) {
      groupError = errorMessage(err, 'Could not bind that group.')
    }
  }

  async function unbindGroup(b: ApiGroupBinding) {
    if (!groupPath) return
    const ok = await dialog.confirm({
      title: `Remove ${b.group_name ?? 'this group'}'s ${scopeKind} binding?`,
      body: 'Every member loses this access on their next request. Their other bindings are untouched.',
      confirmLabel: 'Remove binding',
      danger: true,
    })
    if (!ok) return
    groupError = ''
    try {
      await api.deleteScopedGroupBinding(groupPath, b.group_id)
      await loadGroupBindings(groupPath)
    } catch (err) {
      groupError = errorMessage(err, 'Remove failed.')
    }
  }

  /** Groups not already bound at this scope. */
  const bindableGroups = $derived(
    groups.filter(g => !groupBindings.some(b => b.group_id === g.id)),
  )

  const email = (uid: string) => users.find(u => u.id === uid)?.email ?? `${uid.slice(0, 8)}…`

  /** Group-derived access, indexed by user and reduced to the highest role. A
   *  user in two granting groups keeps the strongest, and we remember every
   *  group so the row can say where it came from. */
  const derivedByUser = $derived.by(() => {
    const m = new Map<string, { role: GroupRole; groups: string[] }>()
    for (const d of derivedMembers) {
      const cur = m.get(d.user_id)
      if (!cur) {
        m.set(d.user_id, { role: d.role, groups: [d.via_group_name] })
        continue
      }
      cur.groups.push(d.via_group_name)
      if (roleRank[d.role] > roleRank[cur.role]) cur.role = d.role
    }
    return m
  })

  interface Row {
    user: UserInfo
    /** The binding bound directly to this user; null if they have none here. */
    direct: Role | null
    /** The strongest role held through a group, and which groups granted it. */
    derived: GroupRole | null
    derivedGroups: string[]
    /** What the server will actually allow — the union of the two. */
    effective: Role | null
  }

  function effectiveOf(direct: Role | null, derived: GroupRole | null): Role | null {
    if (!direct) return derived
    if (!derived) return direct
    return roleRank[derived] > roleRank[direct] ? derived : direct
  }

  const rows = $derived.by((): Row[] => {
    const roleByUser = new Map(members.map(m => [m.user_id, m.role]))
    return users
      .filter(u => !u.disabled)
      .map(u => {
        const direct = roleByUser.get(u.id) ?? null
        const via = derivedByUser.get(u.id)
        const derived = via?.role ?? null
        return {
          user: u,
          direct,
          derived,
          derivedGroups: via?.groups ?? [],
          effective: effectiveOf(direct, derived),
        }
      })
      .sort((a, b) => (roleRank[b.effective ?? 'viewer'] ?? -1) - (roleRank[a.effective ?? 'viewer'] ?? -1))
  })
  /* bindings whose user we can't resolve (e.g. non-admin listUsers) still show —
     direct and group-derived alike, so neither is silently dropped */
  const orphanMembers = $derived(members.filter(m => !users.some(u => u.id === m.user_id)))
  const orphanDerived = $derived.by(() =>
    [...derivedByUser.entries()]
      .filter(([uid]) => !users.some(u => u.id === uid) && !members.some(m => m.user_id === uid))
      .map(([uid, v]) => ({ user_id: uid, ...v })),
  )

  async function invite(e: SubmitEvent) {
    e.preventDefault()
    inviteError = ''
    try {
      const res = await api.createUser(inviteEmail.trim())
      invited = { email: res.email, password: res.password }
      inviteEmail = ''
      users = await api.listUsers().catch(() => users)
    } catch (err) {
      inviteError = errorMessage(err, 'Could not create the user.')
    }
  }

  async function setRole(uid: string, role: Role) {
    if (!scopePath) return
    error = ''
    try {
      await api.putScopedMember(scopePath, uid, role)
      await load(scopePath)
    } catch (err) {
      error = errorMessage(err, 'Role change failed.')
    }
  }

  async function removeBinding(uid: string) {
    if (!scopePath) return
    const ok = await dialog.confirm({
      title: `Remove ${email(uid)}'s ${scopeKind} binding?`,
      body: 'They keep any bindings at other scopes; access unions most-permissively.',
      confirmLabel: 'Remove binding',
      danger: true,
    })
    if (!ok) return
    error = ''
    try {
      await api.deleteScopedMember(scopePath, uid)
      await load(scopePath)
    } catch (err) {
      error = errorMessage(err, 'Remove failed.')
    }
  }

  async function unlock(u: UserInfo) {
    const window = u.locked_until ? ` (auto-unlocks ${relTime(u.locked_until)})` : ''
    const ok = await dialog.confirm({
      title: `Unlock ${u.email}?`,
      body: `Clears the temporary lockout so this account can sign in again immediately${window}.`,
      confirmLabel: 'Unlock account',
      danger: true,
    })
    if (!ok) return
    error = ''
    try {
      await api.unlockUser(u.id)
      users = await api.listUsers().catch(() => users)
    } catch (err) {
      error = errorMessage(err, 'Unlock failed.')
    }
  }

  const scopeLabel = $derived(
    scopeKind === 'instance'
      ? 'instance'
      : scopeKind === 'project'
        ? registry.findProject(pid)?.name ?? '…'
        : `${registry.findProject(pid)?.name ?? '…'} / ${envOptions.find(e => e.id === eid)?.slug ?? '…'}`,
  )
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Office · deny-by-default RBAC · viewer ⊂ developer ⊂ admin ⊂ owner · top-down inheritance</p>
      <h1>Members</h1>
    </div>
    <button class="btn btn-primary" onclick={() => { inviting = !inviting; invited = null }}>+ Invite member</button>
  </header>
  <hr class="ledger-rule" />

  <div class="scope-bar rise" style="animation-delay: 40ms">
    <div class="seg" role="group" aria-label="Binding scope">
      {#each ['instance', 'project', 'environment'] as k}
        <button class="seg-btn" class:on={scopeKind === k} onclick={() => (scopeKind = k as ScopeKind)}>{k}</button>
      {/each}
    </div>
    {#if scopeKind !== 'instance'}
      <select class="select" bind:value={pid}>
        {#each registry.projects as p}<option value={p.id}>{p.name}</option>{/each}
      </select>
    {/if}
    {#if scopeKind === 'environment'}
      <select class="select" bind:value={eid}>
        {#each envOptions as e}<option value={e.id}>{e.slug}</option>{/each}
      </select>
    {/if}
    <span class="folio">bindings at: <strong>{scopeLabel}</strong></span>
  </div>

  {#if inviting}
    <div class="sheet invite rise">
      {#if invited}
        <div class="minted">
          <span class="stamp ok flat">Created — password shown exactly once</span>
          <code class="mono once">{invited.email}</code>
          <code class="mono once pw" data-testid="invited-password">{invited.password}</code>
          <button class="btn btn-sm" onclick={() => navigator.clipboard.writeText(invited!.password)}>Copy password</button>
          <button class="btn btn-sm btn-ghost" onclick={() => { inviting = false; invited = null }}>Done</button>
        </div>
      {:else}
        <form onsubmit={invite}>
          <div class="field grow">
            <label class="label" for="inv-email">Email</label>
            <input id="inv-email" class="input" type="email" bind:value={inviteEmail} placeholder="new@company.dev" required />
          </div>
          <button class="btn btn-stamp" type="submit" disabled={!inviteEmail.trim()}>Create user</button>
          {#if inviteError}<p class="error">{inviteError}</p>{/if}
        </form>
      {/if}
    </div>
  {/if}

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 80ms">
    <table class="ledger" aria-label="Members and their roles" data-testid="members-table">
      <thead>
        <tr>
          <th scope="col">Member</th>
          <th scope="col" style="width: 130px">Last login</th>
          <th scope="col" style="width: 150px">Role at {scopeKind}</th>
          <th scope="col" style="width: 210px">Source</th>
          <th scope="col" style="width: 180px">Direct binding</th>
          <th scope="col" style="width: 110px"></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as row (row.user.id)}
          <tr>
            <td class="who">
              <span class="avatar">{row.user.email.slice(0, 2).toUpperCase()}</span>
              <span class="m-name">{row.user.email}</span>
              {#if row.user.locked}
                <span class="pill pill-locked" title={row.user.locked_until ? `Auto-unlocks ${relTime(row.user.locked_until)}` : 'Temporarily locked'}>Locked</span>
              {/if}
            </td>
            <td>
              {#if row.user.last_login_at}
                <span class="folio">{relTime(row.user.last_login_at)}</span>
              {:else}
                <span class="folio muted">never</span>
              {/if}
            </td>
            <td>
              {#if row.effective}
                <span class="role role-{row.effective}">{row.effective}</span>
                {#if row.effective === 'owner' && scopeKind === 'instance'}<span class="folio guard">never-lock-out</span>{/if}
              {:else}
                <span class="folio">no access</span>
              {/if}
            </td>
            <td class="source">
              {#if row.direct && row.derived}
                <span class="pill src-direct">direct</span>
                <span class="pill src-group" title={row.derivedGroups.join(', ')}>
                  via {row.derivedGroups[0]}{row.derivedGroups.length > 1 ? ` +${row.derivedGroups.length - 1}` : ''}
                </span>
              {:else if row.derived}
                <span class="pill src-group" title={row.derivedGroups.join(', ')}>
                  via {row.derivedGroups[0]}{row.derivedGroups.length > 1 ? ` +${row.derivedGroups.length - 1}` : ''}
                </span>
              {:else if row.direct}
                <span class="pill src-direct">direct</span>
              {:else}
                <span class="folio muted">—</span>
              {/if}
            </td>
            <td>
              <select class="select" value={row.direct ?? ''} onchange={(e) => setRole(row.user.id, (e.currentTarget as HTMLSelectElement).value as Role)}>
                <option value="" disabled>{row.direct ? 'change…' : 'add direct…'}</option>
                <option value="viewer">viewer</option>
                <option value="developer">developer</option>
                <option value="admin">admin</option>
                <option value="owner">owner</option>
              </select>
            </td>
            <td class="row-actions">
              {#if row.user.locked}
                <button class="btn btn-ghost btn-sm unlock-btn" onclick={() => unlock(row.user)}>Unlock</button>
              {/if}
              {#if row.direct}
                <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeBinding(row.user.id)}>Remove</button>
              {:else if row.derived}
                <!-- Nothing to remove here: the grant lives on the group, so
                     send the admin where it can actually be changed. -->
                <a class="btn btn-ghost btn-sm" href="/groups">Groups →</a>
              {/if}
            </td>
          </tr>
        {/each}
        {#each orphanMembers as m (m.user_id)}
          <tr>
            <td class="who"><span class="avatar">?</span><span class="m-name mono">{m.user_id.slice(0, 8)}…</span></td>
            <td><span class="folio muted">—</span></td>
            <td><span class="role role-{m.role}">{m.role}</span></td>
            <td class="source"><span class="pill src-direct">direct</span></td>
            <td></td>
            <td class="row-actions">
              <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeBinding(m.user_id)}>Remove</button>
            </td>
          </tr>
        {/each}
        {#each orphanDerived as d (d.user_id)}
          <tr>
            <td class="who"><span class="avatar">?</span><span class="m-name mono">{d.user_id.slice(0, 8)}…</span></td>
            <td><span class="folio muted">—</span></td>
            <td><span class="role role-{d.role}">{d.role}</span></td>
            <td class="source">
              <span class="pill src-group" title={d.groups.join(', ')}>
                via {d.groups[0]}{d.groups.length > 1 ? ` +${d.groups.length - 1}` : ''}
              </span>
            </td>
            <td></td>
            <td class="row-actions"><a class="btn btn-ghost btn-sm" href="/groups">Groups →</a></td>
          </tr>
        {/each}
        {#if !rows.length && !orphanMembers.length && !orphanDerived.length}
          <tr><td colspan="6" class="empty folio">{loading ? 'Reading…' : 'No members visible for this scope.'}</td></tr>
        {/if}
      </tbody>
    </table>
  </div>

  {#if derivedTruncated}
    <p class="error rise">
      This scope has more group-derived members than one page can resolve, so the
      Source column is incomplete. Narrow the scope, or read the group's members
      on the Groups screen.
    </p>
  {/if}

  <section class="groups rise" style="animation-delay: 110ms">
    <div class="sec-head">
      <h2>Groups at {scopeKind}</h2>
      {#if bindableGroups.length}
        <div class="bind-row">
          <select class="select" bind:value={bindGroupId} aria-label="Group to bind">
            <option value="">bind a group…</option>
            {#each bindableGroups as g}<option value={g.id}>{g.name}</option>{/each}
          </select>
          <select class="select role-sel" bind:value={bindRole} aria-label="Role for the group">
            <option value="viewer">viewer</option>
            <option value="developer">developer</option>
            <option value="admin">admin</option>
          </select>
          <button class="btn btn-sm" onclick={bindGroup} disabled={!bindGroupId}>Bind</button>
        </div>
      {/if}
    </div>
    {#if groupError}<p class="error">{groupError}</p>{/if}

    <div class="sheet table-wrap">
      <table class="ledger" aria-label="Group bindings at this scope" data-testid="group-bindings-table">
        <thead>
          <tr>
            <th scope="col">Group</th>
            <th scope="col" style="width: 90px">Kind</th>
            <th scope="col" style="width: 220px">Role at {scopeKind}</th>
            <th scope="col" style="width: 110px"></th>
          </tr>
        </thead>
        <tbody>
          {#each groupBindings as b (b.group_id)}
            <tr>
              <td><span class="m-name">{b.group_name ?? b.group_id.slice(0, 8) + '…'}</span></td>
              <td>{#if b.group_kind}<span class="pill kind-{b.group_kind}">{b.group_kind}</span>{/if}</td>
              <td><span class="role role-{b.role}">{b.role}</span></td>
              <td class="row-actions">
                <button class="btn btn-ghost btn-sm del-btn" onclick={() => unbindGroup(b)}>Remove</button>
              </td>
            </tr>
          {/each}
          {#if !groupBindings.length}
            <tr><td colspan="4" class="empty folio">No group holds a binding at this scope.</td></tr>
          {/if}
        </tbody>
      </table>
    </div>
  </section>

  <p class="foot-note folio">
    <strong>Role at {scopeKind}</strong> is what the server will actually allow: the union of the
    user's own binding and any held through a group. <strong>Source</strong> says which, so a role
    nobody granted directly is never a mystery. The dropdown and Remove act on the
    <em>direct</em> binding only — group-derived access is changed on the group, not here.
    An instance binding applies everywhere; a project binding covers that project's environments
    and configs; roles union most-permissively, with no precedence between the two sources. You
    cannot grant a role above your own, a group can never be granted owner, and the last instance
    owner can never be removed.
  </p>
</div>

<style>
  .page-n { max-width: 1100px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); }
  .page-head h1 { margin-top: var(--s1); }

  .scope-bar { display: flex; align-items: center; gap: var(--s3); margin-top: var(--s4); flex-wrap: wrap; }
  .scope-bar .select { max-width: 200px; }
  .seg { display: flex; border: 1px solid var(--rule-strong); border-radius: var(--radius); overflow: hidden; }
  .seg-btn {
    font-family: var(--font-ui);
    font-size: var(--text-xs);
    font-weight: 650;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    padding: 0.4rem 0.9rem;
    background: var(--paper-high);
    border: 0;
    border-right: 1px solid var(--rule);
    cursor: pointer;
    color: var(--ink-faint);
  }
  .seg-btn:last-child { border-right: 0; }
  .seg-btn.on { background: var(--ink); color: var(--paper-high); }

  .invite { padding: var(--s4) var(--s5); margin-top: var(--s4); border-left: 4px solid var(--vermilion); }
  .invite form { display: flex; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .field { display: flex; flex-direction: column; gap: var(--s2); }
  .field.grow { flex: 1; min-width: 220px; }
  .error { color: var(--vermilion); font-size: var(--text-sm); width: 100%; margin-top: var(--s3); }
  .minted { display: flex; align-items: center; gap: var(--s3); flex-wrap: wrap; }
  .once {
    background: var(--paper-low);
    border: 1px dashed var(--rule-strong);
    border-radius: var(--radius);
    padding: var(--s1) var(--s3);
    font-size: var(--text-xs);
  }
  .once.pw { color: var(--vermilion); font-weight: 600; }

  .table-wrap { overflow-x: auto; margin-top: var(--s4); }

  .who { display: flex; align-items: center; gap: var(--s3); }
  .avatar {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px; height: 34px;
    border-radius: 50%;
    border: 1.5px solid var(--ink);
    font-weight: 700;
    font-size: 0.7rem;
    letter-spacing: 0.04em;
    background: var(--paper-low);
    flex: none;
  }
  .m-name { font-weight: 620; }

  .role {
    font-size: var(--text-xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.1em;
  }
  .role-owner { color: var(--vermilion); }
  .role-admin { color: var(--archivist); }
  .role-developer { color: var(--verdigris); }
  .role-viewer { color: var(--ink-faint); }
  .guard { display: block; font-size: 0.58rem; }

  .select { max-width: 180px; }
  .row-actions { text-align: right; display: flex; gap: var(--s2); justify-content: flex-end; }
  .del-btn:hover { color: var(--vermilion); }
  .unlock-btn { color: var(--vermilion); }
  .unlock-btn:hover { color: var(--vermilion); text-decoration: underline; }

  .pill-locked {
    color: var(--vermilion);
    background: var(--vermilion-wash);
    margin-left: var(--s2);
  }
  .muted { color: var(--ink-faint); }
  .empty { text-align: center; padding: var(--s6) !important; }

  .groups { margin-top: var(--s6); }
  .sec-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .sec-head h2 { margin: 0; font-size: var(--text-lg); }
  .bind-row { display: flex; gap: var(--s2); flex-wrap: wrap; }
  .bind-row .select { max-width: 190px; }
  .role-sel { max-width: 140px; }
  .kind-oidc { color: var(--archivist); background: var(--archivist-wash); }
  .kind-local { color: var(--verdigris); background: var(--verdigris-wash); }

  .source { display: flex; flex-wrap: wrap; gap: var(--s2); align-items: center; }
  .src-direct { color: var(--ink-faint); background: var(--paper-low); }
  .src-group { color: var(--archivist); background: var(--archivist-wash); }
  .foot-note { margin-top: var(--s3); max-width: 72ch; }
</style>
