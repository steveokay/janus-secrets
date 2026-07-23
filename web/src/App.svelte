<script lang="ts">
  import { router, linkHandler, match } from './lib/router.svelte'
  import { session } from './lib/session.svelte'
  import { registry } from './lib/registry.svelte'
  import Shell from './shell/Shell.svelte'
  import DialogHost from './components/DialogHost.svelte'
  import Init from './screens/Init.svelte'
  import Unseal from './screens/Unseal.svelte'
  import Login from './screens/Login.svelte'
  import Home from './screens/Home.svelte'
  import Projects from './screens/Projects.svelte'
  import ProjectBoard from './screens/ProjectBoard.svelte'
  import SecretEditor from './screens/SecretEditor.svelte'
  import Audit from './screens/Audit.svelte'
  import Tokens from './screens/Tokens.svelte'
  import Members from './screens/Members.svelte'
  import Transit from './screens/Transit.svelte'
  import Operations from './screens/Operations.svelte'
  import Compare from './screens/Compare.svelte'
  import Settings from './screens/Settings.svelte'
  import Approvals from './screens/Approvals.svelte'
  import Trash from './screens/Trash.svelte'
  import Integrations from './screens/Integrations.svelte'
  import Notifications from './screens/Notifications.svelte'
  import NotFound from './screens/NotFound.svelte'

  const configMatch = $derived(match('/projects/:pid/configs/:cid', router.path))
  const projectMatch = $derived(match('/projects/:pid', router.path))

  $effect(() => {
    void session.bootstrap()
  })

  $effect(() => {
    if (session.phase === 'ready') void registry.hydrate()
    else if (session.phase === 'login' || session.phase === 'sealed') registry.reset()
  })
</script>

<svelte:window onclick={linkHandler} />

<DialogHost />

{#if session.phase === 'loading'}
  <div class="boot"><p class="folio">Reaching the registry…</p></div>
{:else if session.phase === 'unreachable'}
  <div class="boot">
    <p class="folio">FOL. 503</p>
    <h1>The registry is unreachable.</h1>
    <p class="boot-sub">The Janus server is not answering. Start it and <button class="btn" onclick={() => session.bootstrap()}>try again</button>.</p>
  </div>
{:else if session.phase === 'uninitialized'}
  <Init />
{:else if session.phase === 'sealed'}
  <Unseal />
{:else if session.phase === 'login'}
  <Login />
{:else}
  <Shell>
    {#if router.path === '/'}
      <Home />
    {:else if router.path === '/projects'}
      <Projects />
    {:else if configMatch}
      <SecretEditor projectId={configMatch.pid} configId={configMatch.cid} />
    {:else if projectMatch}
      <ProjectBoard projectId={projectMatch.pid} />
    {:else if router.path === '/audit'}
      <Audit />
    {:else if router.path === '/tokens'}
      <Tokens />
    {:else if router.path === '/members'}
      <Members />
    {:else if router.path === '/transit'}
      <Transit />
    {:else if router.path === '/operations'}
      <Operations />
    {:else if router.path === '/compare'}
      <Compare />
    {:else if router.path === '/settings'}
      <Settings />
    {:else if router.path === '/approvals'}
      <Approvals />
    {:else if router.path === '/trash'}
      <Trash />
    {:else if router.path === '/integrations'}
      <Integrations />
    {:else if router.path === '/notifications'}
      <Notifications />
    {:else}
      <NotFound />
    {/if}
  </Shell>
{/if}

<style>
  .boot {
    min-height: 100vh;
    display: grid;
    place-content: center;
    text-align: center;
    gap: var(--s3);
  }
  .boot-sub { color: var(--ink-soft); display: flex; align-items: center; gap: var(--s3); }
</style>
