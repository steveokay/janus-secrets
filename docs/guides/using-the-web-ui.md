# How-to: first run and daily use of the web UI

Everything here happens in the browser at your server's address (dev:
`http://127.0.0.1:8220`). For the system behind these screens see
[web.md](../web.md).

## First run — initialize the vault from the browser

1. Open the UI against a fresh server. You land on **"Found the registry."**
2. Pick the Shamir split (default **5 shares, threshold 3**) and the first
   registrar's email, then *Initialize the vault*.
3. The shares and the one-time admin password appear **exactly once**. Copy
   them somewhere durable (password manager, sealed envelopes — the shares
   ARE the master key). Tick the acknowledgement, *Proceed to unseal*.
4. Present any 3 shares — the keyholes fill as each lands (shares submitted
   via `janus unseal` count too). On the third, the vault unseals.
5. Sign in with the one-time password, then change it under **Settings →
   Account** immediately.

The server starts **sealed after every restart** — you'll repeat step 4 (or
configure `JANUS_SEAL_TYPE=awskms` for auto-unseal).

## Daily driving

- **Ctrl+K** — command palette: jump to any project, config, or page; toggle
  the theme; export the audit ledger.
- **Day / Night** in the top bar switches Daylight ↔ Nightwatch, persisted
  per browser.
- The **Overview** in-tray surfaces what needs a human: failing rotations,
  sync errors, leases about to expire, denied requests. An empty tray means
  the schedulers are healthy.
- **Chain verified** in the top bar re-checks the audit hash chain on every
  load; if it ever reports broken, stop and investigate — that is tamper
  evidence, not a glitch.
- Everything destructive asks first via an in-app modal that states the
  consequence; nothing fires from a bare click.

## Where things live

| I want to… | Go to |
|---|---|
| add/edit/reveal secrets | Projects → project → config tile |
| move config values between envs | drag the tile, or the editor's *Promote →* ([guide](promoting-environments.md)) |
| paste an existing `.env` | editor → *Import…* ([guide](import-export.md)) |
| mint a machine token | Service tokens ([guide](service-tokens.md)) |
| give a teammate access | Members ([guide](members-and-rbac.md)) |
| set up SSO or keyless CI | Integrations ([guide](sso-and-federation.md)) |
| rotation / sync / dynamic creds | Operations ([guide](operations-console.md)) |
| recover something deleted | Trash ([guide](trash-and-recovery.md)) |
| rotate/rekey the master key, backups | Settings ([guide](master-key-and-backup.md)) |
| review or revoke your logged-in devices | Settings → **Active sessions** |
| get alerted on failures / pending approvals | Notifications ([guide](notifications.md)) |
