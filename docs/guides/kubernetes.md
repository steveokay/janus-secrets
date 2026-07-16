# Kubernetes integration

This is a task-oriented guide to getting a config's secrets into a
Kubernetes cluster and keeping them fresh. For the provider **reference**
(prune semantics, change detection, credential masking, backoff, backup),
see [sync.md](../ops/sync.md) — this page links to it rather than repeating it.

## Do I need a Kubernetes controller?

**No — not for the sync itself.** Janus is the reconciler. The Janus
server runs an in-process scheduler that **pushes** to your cluster's API
server on an interval (server-side apply). There is nothing to install or
run inside the cluster for Janus to create and update a `Secret`. Janus
never reads back from the cluster.

The one thing Kubernetes does *not* do on its own is roll your workloads
when a `Secret` changes. If you need a Janus edit to reach an
**already-running pod** automatically, you need *a* mechanism for that —
but it's a generic, off-the-shelf one (e.g. Stakater Reloader), not
anything Janus-specific you have to build. See
[Refreshing running workloads](#refreshing-running-workloads) below.

```
   Janus server                         Kubernetes API server
  ┌──────────────┐   PATCH (SSA)       ┌─────────────────────┐
  │ sync target  │ ──────────────────▶ │ Secret myapp/…      │
  │ interval=60s │  fieldManager=janus │ (create or update)  │
  └──────────────┘                     └─────────────────────┘
        ▲                                        │
        │ edit secret in Janus                   │ consumed by
        │ (fingerprint changes)                  ▼
   next tick re-applies                     your pods
```

## What "auto-refresh" reaches

There are two distinct layers, and it's important not to conflate them:

1. **The `Secret` object** — Janus keeps this up to date automatically.
   Edit a value in Janus, and on the next scheduler tick (≤ your
   `--interval-seconds`) the destination `Secret` is re-applied. Change
   detection means unchanged configs cost zero API calls. This layer needs
   **no controller**.
2. **The running pod** — Kubernetes does **not** restart pods when a
   `Secret` they consume changes. What you get depends on how the pod
   consumes the Secret (see [Refreshing running
   workloads](#refreshing-running-workloads)).

So "auto-refresh the Secret" is built in; "auto-refresh the app" needs one
more piece.

## Setup

### 1. Grant Janus least-privilege access in the cluster

Create a `ServiceAccount` scoped to the single namespace you're targeting,
with an RBAC `Role`/`RoleBinding` granting only `get`, `create`, and
`patch` (server-side apply) on `secrets` — nothing cluster-wide, no other
verbs or resources.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata: { name: janus-sync, namespace: myapp }
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: { name: janus-secret-writer, namespace: myapp }
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "create", "patch"]   # patch = server-side apply
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: { name: janus-secret-writer, namespace: myapp }
subjects: [{ kind: ServiceAccount, name: janus-sync, namespace: myapp }]
roleRef:
  kind: Role
  name: janus-secret-writer
  apiGroup: rbac.authorization.k8s.io
```

### 2. Collect the three credentials Janus needs

Janus verifies the API server's TLS against the cluster CA — it does
**not** trust the system root store for the k8s endpoint and does **not**
support skipping verification. An empty CA is rejected at
target-creation time (`400`), before anything is persisted.

```sh
# API server URL
APIURL=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')

# Cluster CA (PEM)
CACERT=$(kubectl config view --raw --minify \
  -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d)

# A bound token for the ServiceAccount (rotate before expiry; see note below)
TOKEN=$(kubectl -n myapp create token janus-sync --duration=8760h)
```

> **Token lifetime.** `kubectl create token` mints a **bound, expiring**
> token. Choose a duration your cluster permits and rotate it before it
> expires with `janus sync update <id> --k8s-token <new>`. If you need a
> non-expiring token, provision a legacy Secret-backed ServiceAccount
> token instead — at the cost of a longer-lived credential.

### 3. Create the sync target in Janus

`--prune` defaults to `true`, which is what you want for a Janus-managed
`Secret`: it lets Janus **create** the `Secret` if absent, update it, and
delete keys you remove from the config (server-side apply owns the
`fieldManager=janus` fields). See
[sync.md § Full-mirror model & prune](../ops/sync.md#full-mirror-model--prune)
for why `--prune=false` cannot create a `Secret` and lets deleted keys
linger.

```sh
janus sync create --config $CONFIG_ID --provider k8s \
  --api-url "$APIURL" \
  --ca-cert "$CACERT" \
  --k8s-token "$TOKEN" \
  --namespace myapp \
  --secret-name myapp-secrets \
  --interval-seconds 60 \
  --prune true
```

Within one interval Janus creates `myapp-secrets` in namespace `myapp` and
keeps it mirrored. `janus sync sync <id>` forces an immediate reconcile
(skips change detection) — useful right after an edit to confirm the push.

Requires the `sync:manage` permission (project **admin**/**owner**). The
k8s bearer token and CA are envelope-encrypted at rest and never echoed
back by the API, logs, or `last_error`.

### 4. Consume the Secret in your workload

Standard Kubernetes — Janus writes an ordinary `Opaque` `Secret`:

```yaml
# Whole config as env vars:
envFrom:
  - secretRef: { name: myapp-secrets }

# Or specific keys:
env:
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef: { name: myapp-secrets, key: DATABASE_URL }

# Or mounted as files:
volumes:
  - name: secrets
    secret: { secretName: myapp-secrets }
```

## Refreshing running workloads

Janus updates the `Secret`; getting that into a live pod depends on the
consumption mode:

| Consumption mode | Sees a Janus update without a restart? |
|---|---|
| `envFrom` / `secretKeyRef` (env vars) | **No** — env is fixed at pod start; needs a pod restart. |
| Mounted `secret` volume | **Eventually** — the kubelet refreshes the file (~1 min), but the app must **re-read** the file. |

Two ways to close the gap into the running app:

1. **Add a reloader (most common).** Deploy something like
   [Stakater Reloader](https://github.com/stakater/Reloader) and annotate
   the workload; it does a rolling restart whenever the referenced
   `Secret` changes. This is the "controller," but it's generic and
   off-the-shelf — **not** a Janus component you build:

   ```yaml
   apiVersion: apps/v1
   kind: Deployment
   metadata:
     name: myapp
     annotations:
       reloader.stakater.com/auto: "true"   # roll on referenced Secret/ConfigMap change
   ```

   Flow: edit in Janus → next tick updates the `Secret` → Reloader rolls
   the Deployment → new pods start with fresh env.

2. **Mount as a volume and re-read in-app.** Point the app at the mounted
   file and watch it (or re-read on a timer / SIGHUP). The kubelet
   propagates the new file within its sync period; no rollout needed. Best
   for apps that already support live config reload.

> **Alternative to k8s Secrets entirely:** run the app under `janus run --`
> as its entrypoint so it fetches secrets live at startup (see
> [cli.md](../cli.md)). No static `Secret` object, but a refresh still
> requires a pod restart, and the pod needs network access + a service
> token to reach Janus.

## End-to-end timing

From "edit in Janus" to "new value in a fresh pod", with the Reloader
option:

```
edit in Janus
      │  ≤ JANUS_SYNC_TICK / --interval-seconds
      ▼
Secret updated in the cluster (server-side apply)
      │  Reloader detects the change
      ▼
Deployment rolling-restarts
      │  new pod scheduled + ready
      ▼
app running with the new value
```

Tune `--interval-seconds` per target and `JANUS_SYNC_TICK` on the server
(see [sync.md § Scheduler](../ops/sync.md#scheduler)) for how fast the first hop
runs; the rollout hop is governed by your Deployment's update strategy.

## Operational notes

- **Sealed server:** while sealed, the scheduler is a complete no-op and a
  manual sync returns the `sealed` error — no push happens until unseal.
  See [sync.md § Sealed behavior](../ops/sync.md#sealed-behavior).
- **Failures back off** exponentially (1 min → 1 h cap) and flip the
  target to `failed` after 5 consecutive failures; reactivate with
  `janus sync sync <id>` or `janus sync update <id> --status active`. See
  [sync.md § Failure handling & backoff](../ops/sync.md#failure-handling--backoff).
- **Cross-project references are refused** during a synced resolve, by
  design — keep every reference in a synced config scoped to its own
  project. See
  [sync.md § Cross-project references refused](../ops/sync.md#cross-project-references-refused-security).
- **Audit:** every reconcile writes a value-free `sync.reconcile` event
  under a `system` actor; create/update/delete/sync API calls are audited
  under the calling user.
- **Backup/restore:** the target (with its envelope-encrypted k8s token
  and CA) travels with `janus backup` / `janus restore` and resumes
  reconciling after unseal. See [backup-restore.md](../ops/backup-restore.md).
