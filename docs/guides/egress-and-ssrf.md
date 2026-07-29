# Outbound egress & the SSRF guard

Janus makes outbound calls on your behalf: notification webhooks and Slack,
SMTP, rotation webhooks and database dials, all eight sync providers, and OIDC
discovery/JWKS. Every one of those destinations is **operator-configured**,
which is exactly why they are guarded — a mis-configured or maliciously
configured integration should not be able to make the server fetch its own
cloud instance-metadata endpoint and hand back credentials.

This guide is task-oriented. For where the guard sits in the threat model, see
[threat-model.md](../threat-model.md); for the full variable reference, see
[production deployment](production-deployment.md#outbound-integrations-egress-ssrf-guard).

## How the guard works

Every outbound client dials through one shared hardened dialer. The check runs
at **connect time**, on the address the kernel is about to dial — not on the
URL. That distinction is the whole point: a hostname can resolve to a harmless
address when you validate it and to `169.254.169.254` a moment later when the
connection is actually made (DNS rebinding). Re-checking the resolved IP on
every dial, including every redirect hop, closes that window.

Three tiers, from least to most configurable:

| Tier | Ranges | Configurable? |
|---|---|---|
| Always blocked | link-local (`169.254.0.0/16`, `fe80::/10`), the IPv6 IMDS address `fd00:ec2::254`, unspecified, multicast | **No** |
| Blocked on request | loopback, RFC1918, ULA — i.e. "private space" | `JANUS_OUTBOUND_BLOCK_PRIVATE` |
| Exempt from the row above | whatever you name | `JANUS_OUTBOUND_ALLOW` |

Private space is **allowed by default**, because Janus is self-hosted software
that legitimately talks to in-cluster services, LAN SMTP relays and internal
Postgres. Turning it off is a hardening step, not the baseline.

## Task: block private space but still reach one internal service

This is the common case, and the reason the allowlist exists. Set both
variables:

```sh
JANUS_OUTBOUND_BLOCK_PRIVATE=true
JANUS_OUTBOUND_ALLOW=10.96.0.1/32
```

Entries are comma-separated IP addresses or CIDR prefixes, in either family. A
bare address is treated as a single host (`10.96.0.1` ≡ `10.96.0.1/32`), and a
prefix with host bits set is normalised to its network (`10.96.0.1/24` becomes
`10.96.0.0/24`). Blank fields are ignored, so a trailing comma is harmless.

## Task: run Janus inside the cluster it syncs to

The chart sets `outboundBlockPrivate: true`. `kubernetes.default.svc` is a
ClusterIP in a private range, so without an allowlist the API server is blocked
along with everything else — and the symptoms do not name the cause. The k8s
sync provider reports a sanitized `apply failed`, and service-account
federation returns a generic 401 because the JWKS fetch never leaves the pod.

Find the API server address and allowlist it:

```sh
kubectl get svc kubernetes -n default -o jsonpath='{.spec.clusterIP}'
# 10.96.0.1
```

It is the first address of the service CIDR (`10.96.0.1` on a default kubeadm
or minikube cluster) and is stable for the life of the cluster. In Helm values:

```yaml
env:
  outboundBlockPrivate: true
  outboundAllow: "10.96.0.1/32"
```

> `helm --set` treats commas as list separators, so a multi-entry value needs
> escaping — `--set-string env.outboundAllow='10.96.0.1/32\,10.0.0.0/8'` — or,
> better, put it in a values file.

Setting `outboundBlockPrivate: false` also works and is what earlier versions
of the Kubernetes guide advised. The difference is scope: that reopens **all**
private space, while the allowlist reopens one address.

## Task: change the policy without a restart

**Settings → Outbound policy** (owner only) edits the same two settings and
applies them on the **next dial** — no restart, and every engine picks the change
up at once, because the whole process dials through one policy source.

The stored policy supersedes the environment and survives restarts. The screen
says which of the two is in force ("environment" or "this screen"), so a Helm
chart that disagrees with the running instance is visible rather than silent.
**Use environment** discards the stored policy and goes back to `JANUS_OUTBOUND_*`.

Three things are deliberately not editable there:

- **`allow_proxy`** is shown read-only. It is the one setting that blinds the
  guard, so it stays environment-only; sending it to the API is a `400`, not a
  silent drop.
- **The always-blocked ranges** cannot be exempted from the UI any more than
  from the environment — the API rejects such an entry with the same parser the
  guard uses.
- **Nothing at all**, if you set `JANUS_OUTBOUND_POLICY_LOCKED=true`. That pins
  the policy to the environment and makes every write a `409`.

That last one exists because editing egress from the UI is a real trade. The
guard's job is to bound what a mis- or maliciously-configured integration can
make the server dial, and configuring integrations is already an admin
privilege — so a policy editable in-app sits under the authority it constrains.
The blast radius is bounded (no stored policy can reach the metadata ranges, so
the worst case is private-space reachability, which the *default* policy permits
anyway), and editing is **owner-only**, not admin. But a deployment that
hardened beyond the default should be able to keep the guarantee it chose, and
the lock is how.

## Task: check what policy is actually in force

Three ways, all reporting the live policy the dialers are using.

**Settings → Outbound policy** (owner) shows it and lets you change it.

**Settings → Health** has a read-only **Outbound policy** panel for anyone with
instance `audit:read`, via `GET /v1/sys/status`.

**From the host** — `janus doctor` reports it as the `outbound.ssrf` check,
with the effective policy in full:

```
  PASS  outbound.ssrf     outbound integration calls use the connect-time resolved-IP guard
                            link-local / cloud-metadata ranges (169.254.0.0/16, fe80::/10,
                            fd00:ec2::254) are always blocked at connect time
                            loopback + RFC1918 + ULA are also blocked (JANUS_OUTBOUND_BLOCK_PRIVATE)
                            exempt from the private-space block (JANUS_OUTBOUND_ALLOW): 10.96.0.1/32
```

## What the allowlist deliberately will not do

**It cannot reach the metadata ranges.** The allowlist is consulted only inside
the private-space branch, below the unconditional block, so no entry can permit
`169.254.169.254` however it is written — not as a `/32`, not inside a wider
prefix, not as `0.0.0.0/0`. Naming an always-blocked range is treated as a
configuration error and **fails startup** rather than sitting in your config
looking effective. Cloud instance metadata is the highest-value SSRF target
there is; a typo in an environment variable must not be able to reach it.

**It does not accept hostnames.** `kubernetes.default.svc` is rejected. The
guard's value comes from checking the resolved address; allowlisting a *name*
would mean trusting DNS for that name, which reopens the rebinding attack the
connect-time check exists to defeat. Use the address.

**It does not accept the IPv4-in-IPv6 spelling.** `::ffff:10.0.0.1` is
rejected, because a prefix's bit count is ambiguous under that form and
guessing would silently widen or narrow what you wrote. Write plain IPv4 —
enforcement unmaps the dialled address before matching, so a dial that resolves
to the mapped form still matches.

## Failure modes and what they mean

| Symptom | Cause | Fix |
|---|---|---|
| Server exits at boot naming an entry | The `JANUS_OUTBOUND_ALLOW` value is malformed, or names an always-blocked range | Correct the entry; there is no override |
| `doctor` warns "the allowlist has no effect" | Entries set without `JANUS_OUTBOUND_BLOCK_PRIVATE=true` | Enable the block, or drop the allowlist |
| An integration cannot connect, no other clue | Private space blocked with nothing exempted | Allowlist the destination |
| Sync `apply failed` / federation 401 in-cluster | The API server's ClusterIP is blocked | Allowlist it (above) |
| The environment changed but the policy did not | A **stored** policy supersedes it | Settings → Outbound policy → **Use environment**, or `DELETE /v1/sys/outbound-policy` |
| Saving the policy returns `409` | `JANUS_OUTBOUND_POLICY_LOCKED=true` | Change it in the deployment, or unset the lock |
| Saving returns `400` naming an entry | A hostname, a malformed entry, or an always-blocked range | Use an address; the metadata ranges can never be exempted |

Two behaviours worth knowing. **One bad entry discards the whole list**, rather
than applying the entries that parsed — a partially-applied allowlist that
permits some traffic and silently drops the rest is far harder to diagnose than
a refusal to start. And the allowlist **fails closed** everywhere: if it cannot
be understood, nothing is exempted.

## The proxy escape hatch

`JANUS_OUTBOUND_ALLOW_PROXY=true` restores `HTTP_PROXY` / `HTTPS_PROXY` /
`ALL_PROXY` handling for these clients. It is **off by default and should stay
off**. Through a proxy the TCP connection goes to the proxy, and the *proxy*
resolves and fetches the operator-supplied target — so the connect-time guard
only ever sees the proxy's address and the metadata block stops applying to the
real destination. What remains is a best-effort URL-time check that catches
literal-IP targets only.

The hatch exists for deployments whose only egress path is a proxy. It logs a
startup warning, `janus doctor` reports it as a warning, and the health panel
flags it. If you must use it, enforce destination allowlisting on the proxy.

## Related

- [Kubernetes integration](kubernetes.md) — the in-cluster sync and federation flows.
- [Production deployment](production-deployment.md#outbound-integrations-egress-ssrf-guard) — the full variable reference.
- [Troubleshooting](troubleshooting.md) — symptom → cause across all misconfiguration.
- [Threat model](../threat-model.md) — what this guard is and is not.
