<script lang="ts">
  import {
    api, errorMessage,
    type ApiGroup, type ApiGroupBinding, type ApiGroupMember, type GroupKind, type UserInfo,
  } from '../lib/api'
  import { registry } from '../lib/registry.svelte'
  import { dialog } from '../lib/dialog.svelte'
  import { relTime } from '../lib/util'

  let groups = $state<ApiGroup[]>([])
  let users = $state<UserInfo[]>([])
  let loading = $state(true)
  let error = $state('')

  let selectedId = $state('')
  let members = $state<ApiGroupMember[]>([])
  let bindings = $state<ApiGroupBinding[]>([])
  let detailError = $state('')

  let creating = $state(false)
  let newName = $state('')
  let newKind = $state<GroupKind>('local')
  let newClaim = $state('')
  let newDesc = $state('')
  let createError = $state('')

  let addUserId = $state('')

  const selected = $derived(groups.find(g => g.id === selectedId) ?? null)

  $effect(() => {
    void loadGroups()
    api.listUsers().then(us => (users = us)).catch(() => (users = []))
  })

  $effect(() => {
    if (selectedId) void loadDetail(selectedId)
    else { members = []; bindings = [] }
  })

  async function loadGroups() {
    loading = true
    error = ''
    try {
      groups = await api.listGroups()
    } catch (err) {
      error = errorMessage(err, 'Could not list groups.')
      groups = []
    } finally {
      loading = false
    }
  }

  async function loadDetail(gid: string) {
    detailError = ''
    try {
      const [detail, ms] = await Promise.all([api.getGroup(gid), api.listGroupMembers(gid)])
      bindings = detail.bindings ?? []
      members = ms
    } catch (err) {
      detailError = errorMessage(err, 'Could not read this group.')
      members = []
      bindings = []
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault()
    createError = ''
    try {
      const g = await api.createGroup({
        name: newName.trim(),
        kind: newKind,
        claim_value: newKind === 'oidc' ? newClaim.trim() : undefined,
        description: newDesc.trim() || undefined,
      })
      newName = ''; newClaim = ''; newDesc = ''
      creating = false
      await loadGroups()
      selectedId = g.id
    } catch (err) {
      createError = errorMessage(err, 'Could not create the group.')
    }
  }

  async function remove(g: ApiGroup) {
    const grants = g.binding_count === 1 ? '1 binding' : `${g.binding_count} bindings`
    const ok = await dialog.confirm({
      title: `Delete the group “${g.name}”?`,
      body: g.binding_count
        ? `It grants access at ${grants}. Deleting removes every one of them, so its members lose that access on their next request.`
        : 'It grants no access today. Membership is removed with it.',
      confirmLabel: 'Delete group',
      danger: true,
    })
    if (!ok) return
    error = ''
    try {
      await api.deleteGroup(g.id)
      if (selectedId === g.id) selectedId = ''
      await loadGroups()
    } catch (err) {
      error = errorMessage(err, 'Delete failed.')
    }
  }

  async function addMember() {
    if (!selected || !addUserId) return
    detailError = ''
    try {
      await api.addGroupMember(selected.id, addUserId)
      addUserId = ''
      await Promise.all([loadDetail(selected.id), loadGroups()])
    } catch (err) {
      detailError = errorMessage(err, 'Could not add the member.')
    }
  }

  async function removeMember(uid: string) {
    if (!selected) return
    detailError = ''
    try {
      await api.removeGroupMember(selected.id, uid)
      await Promise.all([loadDetail(selected.id), loadGroups()])
    } catch (err) {
      detailError = errorMessage(err, 'Could not remove the member.')
    }
  }

  const email = (uid: string) => users.find(u => u.id === uid)?.email ?? `${uid.slice(0, 8)}…`

  /** Users not already in the selected group — the add-member picker. */
  const addable = $derived(
    users.filter(u => !u.disabled && !members.some(m => m.user_id === u.id)),
  )

  /** Name the scope a grant reaches. The registry resolves ids the caller can
   *  see; a project they cannot see falls back to the bare level rather than
   *  leaking a name they have no access to. */
  function scopeLabel(b: ApiGroupBinding): string {
    if (b.scope_level === 'instance') return 'instance'
    if (b.scope_level === 'project') {
      return registry.findProject(b.project_id ?? '')?.name ?? 'a project'
    }
    for (const p of registry.projects) {
      const env = (p.environments ?? []).find(e => e.id === b.environment_id)
      if (env) return `${p.name} / ${env.slug}`
    }
    return 'an environment'
  }
</script>

<div class="page-n">
  <header class="page-head rise">
    <div>
      <p class="folio">Office · one binding per team, not one per person · union with direct bindings · never owner</p>
      <h1>Groups</h1>
    </div>
    <button class="btn btn-primary" onclick={() => (creating = !creating)}>+ New group</button>
  </header>
  <hr class="ledger-rule" />

  {#if creating}
    <div class="sheet compose rise">
      <form onsubmit={create}>
        <div class="field grow">
          <label class="label" for="g-name">Name</label>
          <input id="g-name" class="input" bind:value={newName} placeholder="Team Payments" required />
        </div>
        <div class="field">
          <label class="label" for="g-kind">Kind</label>
          <select id="g-kind" class="select" bind:value={newKind}>
            <option value="local">local — explicit member list</option>
            <option value="oidc">oidc — fed by the IdP</option>
          </select>
        </div>
        {#if newKind === 'oidc'}
          <div class="field grow">
            <label class="label" for="g-claim">Claim value</label>
            <input id="g-claim" class="input mono" bind:value={newClaim} placeholder="grp-payments or a GUID" required />
          </div>
        {/if}
        <div class="field grow">
          <label class="label" for="g-desc">Description</label>
          <input id="g-desc" class="input" bind:value={newDesc} placeholder="optional" />
        </div>
        <button class="btn btn-stamp" type="submit" disabled={!newName.trim() || (newKind === 'oidc' && !newClaim.trim())}>
          Create group
        </button>
        {#if createError}<p class="error">{createError}</p>{/if}
        <p class="hint folio">
          {#if newKind === 'oidc'}
            Membership is refreshed from the group claim at each sign-in and cannot be edited here —
            that is what keeps the IdP the complete record of who has this access.
          {:else}
            Membership is managed here. Use this when there is no identity provider, or for
            password logins.
          {/if}
        </p>
      </form>
    </div>
  {/if}

  {#if error}<p class="error rise">{error}</p>{/if}

  <div class="sheet table-wrap rise" style="animation-delay: 60ms">
    <table class="ledger" aria-label="Groups">
      <thead>
        <tr>
          <th scope="col">Group</th>
          <th scope="col" style="width: 90px">Kind</th>
          <th scope="col" style="width: 220px">Claim value</th>
          <th scope="col" style="width: 100px">Members</th>
          <th scope="col" style="width: 100px">Grants</th>
          <th scope="col" style="width: 110px"></th>
        </tr>
      </thead>
      <tbody>
        {#each groups as g (g.id)}
          <tr class:on={selectedId === g.id}>
            <td>
              <button class="link-cell" onclick={() => (selectedId = selectedId === g.id ? '' : g.id)}>
                <span class="g-name">{g.name}</span>
              </button>
              {#if g.description}<span class="folio desc">{g.description}</span>{/if}
            </td>
            <td><span class="pill kind-{g.kind}">{g.kind}</span></td>
            <td>
              {#if g.claim_value}
                <code class="mono claim">{g.claim_value}</code>
              {:else}
                <span class="folio muted">—</span>
              {/if}
            </td>
            <td><span class="folio">{g.member_count}</span></td>
            <td>
              {#if g.binding_count}
                <span class="folio">{g.binding_count}</span>
              {:else}
                <span class="folio muted">no access</span>
              {/if}
            </td>
            <td class="row-actions">
              <button class="btn btn-ghost btn-sm del-btn" onclick={() => remove(g)}>Delete</button>
            </td>
          </tr>
        {/each}
        {#if !groups.length}
          <tr><td colspan="6" class="empty folio">{loading ? 'Reading…' : 'No groups yet.'}</td></tr>
        {/if}
      </tbody>
    </table>
  </div>

  {#if selected}
    <div class="sheet detail rise" style="animation-delay: 90ms">
      <div class="detail-head">
        <h2>{selected.name}</h2>
        <span class="pill kind-{selected.kind}">{selected.kind}</span>
      </div>
      {#if detailError}<p class="error">{detailError}</p>{/if}

      <div class="cols">
        <section>
          <h3 class="sub">Members</h3>
          {#if selected.kind === 'oidc'}
            <p class="folio note">
              From the identity provider, refreshed at each sign-in — so this lists only users who
              have signed in since the group was created. Add or remove people in your IdP.
            </p>
          {:else}
            <div class="add-row">
              <select class="select" bind:value={addUserId} aria-label="User to add">
                <option value="">add a member…</option>
                {#each addable as u}<option value={u.id}>{u.email}</option>{/each}
              </select>
              <button class="btn btn-sm" onclick={addMember} disabled={!addUserId}>Add</button>
            </div>
          {/if}
          <ul class="plain-list">
            {#each members as m (m.user_id)}
              <li>
                <span class="m-name">{email(m.user_id)}</span>
                <span class="folio muted">{relTime(m.created_at)}</span>
                {#if selected.kind === 'local'}
                  <button class="btn btn-ghost btn-sm del-btn" onclick={() => removeMember(m.user_id)}>Remove</button>
                {/if}
              </li>
            {/each}
            {#if !members.length}<li class="folio muted">No members.</li>{/if}
          </ul>
        </section>

        <section>
          <h3 class="sub">Grants</h3>
          <p class="folio note">
            Bind a group from the Members screen, at the scope you want it to apply to.
          </p>
          <ul class="plain-list">
            {#each bindings as b (b.scope_level + b.role + b.created_at)}
              <li>
                <span class="m-name">{scopeLabel(b)}</span>
                <span class="role role-{b.role}">{b.role}</span>
              </li>
            {/each}
            {#if !bindings.length}<li class="folio muted">This group grants no access.</li>{/if}
          </ul>
        </section>
      </div>
    </div>
  {/if}

  <p class="foot-note folio">
    A group binding unions with direct bindings exactly as two direct bindings do — there is no
    precedence between them and no deny rule. A group can hold viewer, developer or admin, never
    owner: owner rotates the master key, prunes the audit chain and destroys secret history, so it
    stays a deliberate direct binding.
  </p>
</div>

<style>
  .page-n { max-width: 1100px; margin: 0 auto; }
  .page-head { display: flex; justify-content: space-between; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .page-head h1 { margin-top: var(--s1); }

  .compose { padding: var(--s4) var(--s5); margin-top: var(--s4); border-left: 4px solid var(--archivist); }
  .compose form { display: flex; align-items: flex-end; gap: var(--s4); flex-wrap: wrap; }
  .field { display: flex; flex-direction: column; gap: var(--s2); }
  .field.grow { flex: 1; min-width: 200px; }
  .hint { width: 100%; margin-top: var(--s3); max-width: 70ch; }
  .error { color: var(--vermilion); font-size: var(--text-sm); width: 100%; margin-top: var(--s3); }

  .table-wrap { overflow-x: auto; margin-top: var(--s4); }
  tr.on { background: var(--paper-low); }
  .link-cell {
    background: none; border: 0; padding: 0; cursor: pointer;
    font: inherit; color: inherit; text-align: left;
  }
  .link-cell:hover .g-name { text-decoration: underline; }
  .g-name { font-weight: 620; }
  .desc { display: block; font-size: 0.62rem; }
  .claim { font-size: var(--text-xs); }

  .kind-oidc { color: var(--archivist); background: var(--archivist-wash); }
  .kind-local { color: var(--verdigris); background: var(--verdigris-wash); }

  .detail { padding: var(--s4) var(--s5); margin-top: var(--s4); }
  .detail-head { display: flex; align-items: center; gap: var(--s3); }
  .detail-head h2 { margin: 0; }
  .cols { display: grid; grid-template-columns: 1fr 1fr; gap: var(--s5); margin-top: var(--s4); }
  .cols section { min-width: 0; }
  .sub {
    font-family: var(--font-ui);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: 0.12em;
    color: var(--ink-faint);
    margin: 0 0 var(--s2);
  }
  .note { max-width: 60ch; margin-bottom: var(--s3); }
  .add-row { display: flex; gap: var(--s2); margin-bottom: var(--s3); }
  .plain-list { list-style: none; margin: 0; padding: 0; }
  .plain-list li {
    display: flex; align-items: center; gap: var(--s3);
    padding: var(--s2) 0; border-bottom: 1px solid var(--rule);
  }
  .m-name { font-weight: 600; flex: 1; min-width: 0; overflow-wrap: anywhere; }

  .role { font-size: var(--text-xs); font-weight: 700; text-transform: uppercase; letter-spacing: 0.1em; }
  .role-admin { color: var(--archivist); }
  .role-developer { color: var(--verdigris); }
  .role-viewer { color: var(--ink-faint); }

  .row-actions { text-align: right; }
  .del-btn:hover { color: var(--vermilion); }
  .muted { color: var(--ink-faint); }
  .empty { text-align: center; padding: var(--s6) !important; }
  .foot-note { margin-top: var(--s3); max-width: 74ch; }

  @media (max-width: 720px) {
    .cols { grid-template-columns: 1fr; }
  }
</style>
