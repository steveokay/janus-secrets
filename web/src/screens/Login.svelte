<script lang="ts">
  import { session } from '../lib/session.svelte'
  import { api, errorMessage, ApiError, type OIDCLoginStatus } from '../lib/api'
  import JanusMark from '../components/JanusMark.svelte'
  import Guilloche from '../components/Guilloche.svelte'

  let email = $state('')
  let password = $state('')
  let totpCode = $state('')
  let totpRequired = $state(false)
  let error = $state('')
  let busy = $state(false)
  let oidc = $state<OIDCLoginStatus | null>(null)

  $effect(() => {
    api.oidcLoginStatus().then(s => (oidc = s)).catch(() => (oidc = null))
  })

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    error = ''
    busy = true
    try {
      await session.login(email.trim(), password, totpRequired ? totpCode.trim() : undefined)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'totp_required') {
        totpRequired = true
        error = ''
      } else {
        error = errorMessage(err, 'Sign-in failed — check your credentials.')
      }
    } finally {
      busy = false
    }
  }
</script>

<div class="gate">
  <div class="rosette" aria-hidden="true">
    <Guilloche size={720} rings={18} opacity={0.1} />
  </div>

  <div class="plate card rise">
    <div class="col brand">
      <JanusMark size={52} />
      <h1>Enter the<br />atrium.</h1>
      <p class="sub">
        The vault is unsealed and standing by. Identify yourself —
        every entrance is recorded.
      </p>
      <span class="stamp ok flat" style="align-self: flex-start">Unsealed</span>
    </div>

    <form class="col" onsubmit={submit}>
      <div class="field">
        <label class="label" for="email">Registrar</label>
        <input id="email" class="field-ruled" type="email" bind:value={email}
          placeholder="you@company.dev" autocomplete="username" />
      </div>
      <div class="field">
        <label class="label" for="pw">Passphrase</label>
        <input id="pw" class="field-ruled" type="password" bind:value={password}
          placeholder="••••••••••••" autocomplete="current-password" />
      </div>

      {#if totpRequired}
        <div class="field">
          <label class="label" for="totp">Two-factor code</label>
          <input id="totp" class="field-ruled mono" bind:value={totpCode}
            placeholder="123456 or a recovery code" autocomplete="one-time-code"
            inputmode="numeric" autocapitalize="off" spellcheck="false" />
          <span class="folio hint">From your authenticator app, or a recovery code.</span>
        </div>
      {/if}

      {#if error}<p class="error">{error}</p>{/if}

      <button class="btn btn-primary wide" type="submit"
        disabled={busy || !email.trim() || !password || (totpRequired && !totpCode.trim())}>
        {busy ? 'Checking the register…' : totpRequired ? 'Verify & sign in' : 'Sign the register'}
      </button>

      {#if oidc?.enabled}
        <div class="divider"><span class="folio">or continue with</span></div>
        <a class="btn oidc-btn" href="/v1/auth/oidc/login">{oidc.name || 'SSO'}</a>
      {/if}
    </form>
  </div>
</div>

<style>
  .gate {
    min-height: 100vh;
    display: grid;
    place-items: center;
    position: relative;
    overflow: hidden;
    padding: var(--s5);
  }
  .rosette {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    color: var(--ink);
    pointer-events: none;
  }

  .card {
    position: relative;
    width: min(760px, 100%);
    display: grid;
    grid-template-columns: 1.1fr 1fr;
  }
  .col { padding: var(--s7); display: flex; flex-direction: column; gap: var(--s4); }

  .brand {
    border-right: 2px dashed var(--rule);
    background:
      radial-gradient(circle at 20% 110%, var(--vermilion-wash), transparent 55%);
  }
  .brand h1 { font-size: var(--text-2xl); }
  .sub { color: var(--ink-soft); font-size: var(--text-sm); }

  form { justify-content: center; }
  .field { display: flex; flex-direction: column; gap: var(--s2); }
  .error { color: var(--vermilion); font-size: var(--text-sm); }
  .wide { justify-content: center; margin-top: var(--s3); }

  .divider {
    text-align: center;
    position: relative;
    margin-top: var(--s2);
  }
  .divider::before {
    content: '';
    position: absolute;
    left: 0; right: 0; top: 50%;
    border-top: 1px solid var(--rule);
  }
  .divider span { position: relative; background: var(--paper-high); padding: 0 var(--s3); }

  .oidc-btn { justify-content: center; }

  @media (max-width: 680px) {
    .card { grid-template-columns: 1fr; }
    .brand { border-right: 0; border-bottom: 2px dashed var(--rule); }
  }
</style>
