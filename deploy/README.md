# `deploy/` — deployment artifacts

Manifests and packaging for running the Janus server on an orchestrator. The
container image itself is built from the repo-root `Dockerfile` (dev) /
`Dockerfile.release` (the published `ghcr.io/steveokay/janus` image); this
folder is about *deploying* that image, not building it.

For the end-to-end, per-target walkthrough (Docker/Compose, Kubernetes, Docker
Swarm, Argo CD/Flux, Nomad, systemd) see
[**Deployment modes**](../docs/guides/production-deployment.md#10-deployment-modes)
in the production-deployment guide. The rest of that guide is the config /
unseal / monitoring reference each mode links into.

## Contents

```
deploy/
  helm/janus/     Helm chart for Kubernetes
```

Raw `kubectl apply` YAML (Secret + Deployment + Service, hardened) lives inline
in the deployment-modes guide rather than as separate files, so there's a
single copy to keep accurate.

## Helm chart (`helm/janus`)

A production-sane chart for a **single-node** Janus + bring-your-own Postgres.
It encodes the deployment invariants so you don't have to remember them:

- `replicaCount: 1` with a **`Recreate`** strategy — Janus is single-node by
  design (HA is a non-goal); a second concurrent process double-runs the
  in-process schedulers.
- Hardened `securityContext` matching the distroless nonroot image (uid/gid
  **65532**, `readOnlyRootFilesystem`, dropped capabilities, `RuntimeDefault`
  seccomp).
- Correct probes: liveness → `/v1/sys/live` (sealed-safe), readiness →
  `/v1/sys/ready` (only when initialized **and** unsealed), optional startup →
  `/v1/sys/live`.
- Per-provider **seal** wiring (`awskms` / `gcpkms` / `azurekv` / `shamir`) and
  a ServiceAccount you annotate for cloud identity (IRSA / GKE Workload
  Identity / Azure AD Workload Identity) so KMS auto-unseal works without
  static credentials.
- Database via an **existing Secret** (`database.existingSecret`, preferred —
  keeps the Postgres DSN out of Helm values) or a chart-created Secret
  (`database.url`).

### Quick start

```sh
# Preferred: reference a DSN Secret you created out-of-band.
kubectl create secret generic janus-db -n janus \
  --from-literal=JANUS_DATABASE_URL='postgres://janus:…@db:5432/janus?sslmode=require'

helm install janus deploy/helm/janus -n janus --create-namespace \
  --set seal.type=awskms \
  --set seal.awskms.keyArn=arn:aws:kms:us-east-1:111122223333:key/abcd-… \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::111122223333:role/janus-kms \
  --set database.existingSecret=janus-db \
  --set image.tag=0.1.0
```

After the first install, run the one-time `janus init` (and `janus unseal` if
you chose Shamir) — the chart's post-install `NOTES` prints the exact commands.
See [Deployment modes §10.2](../docs/guides/production-deployment.md#102-kubernetes).

### Key values

| Value | Purpose | Default |
|---|---|---|
| `image.repository` / `image.tag` / `image.digest` | Which image to run. Pin a tag or digest — never `:latest` (migrations run on boot). | `ghcr.io/steveokay/janus` / `0.1.0` |
| `seal.type` | `awskms` \| `gcpkms` \| `azurekv` \| `shamir`. Cloud-KMS types auto-unseal on boot (recommended for k8s). | `awskms` |
| `seal.awskms.keyArn` / `seal.gcpkms.key` / `seal.azurekv.vaultUrl`+`keyName` | Per-provider key reference. | `""` |
| `serviceAccount.annotations` | Cloud-identity annotations for KMS access (IRSA / GKE WI / Azure WI). | `{}` |
| `database.existingSecret` | Name of a Secret holding `JANUS_DATABASE_URL` (**preferred**). | `""` |
| `database.url` | Fallback DSN the chart wraps in a Secret. | `""` |
| `ingress.enabled` | Expose via an Ingress (terminate TLS here). | `false` |
| `metrics.token` / `metrics.existingSecret` | Enable the Prometheus `/metrics` endpoint. | `""` |
| `postgresql.enabled` | Stand up an **evaluation-only** single-replica Postgres. **Not for production.** | `false` |

The full, commented set lives in
[`helm/janus/values.yaml`](helm/janus/values.yaml).

### Validate the chart

```sh
helm lint deploy/helm/janus
helm template janus deploy/helm/janus                          # default (awskms)
helm template janus deploy/helm/janus --set seal.type=shamir   # shamir variant
```

## Verify before you deploy

Releases are cosign keyless-signed and carry SBOM + SLSA build-provenance
attestations. Verify the image before rolling it out:

```sh
gh attestation verify oci://ghcr.io/steveokay/janus:v0.1.0 --repo steveokay/janus-secrets
```

See [`SECURITY.md`](../SECURITY.md) and the
[production-deployment guide §5](../docs/guides/production-deployment.md#5-running-the-image).
