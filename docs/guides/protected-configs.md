# How-to: protect a config with four-eyes approval

A **protected config** (`require_approval = true`) does not accept direct secret
saves. Instead every save becomes a **pending edit request** that a *different*
person must approve before it commits — the classic four-eyes control for
sensitive environments such as prod.

This reuses the same request → approve → apply machinery as environment
promotion, but for *in-place* edits to one config.

## 1. Mark a config protected

In the secret editor for the config, click **Protect…** in the header (this
requires `promotion:manage`, i.e. admin+ on the project). The button flips to
**🛡 Protected** and a banner appears. Toggle it off the same way.

Via the API:

```
PUT /v1/configs/{cid}/require-approval
{ "enabled": true }
```

Response: `{ "require_approval": true }`. The change is audited value-free as
`config.require_approval.set`.

## 2. Propose an edit

Editing secrets works exactly as before — reveal, change, add or delete keys in
the dirty-state buffer. But on a protected config the Save button reads
**Submit for approval**. Submitting does **not** create a new config version;
it files an edit request and returns `202 Accepted`:

```
PUT /v1/configs/{cid}/secrets           # same batch-write body as usual
→ 202 { "edit_request_id": "…", "status": "pending", "keys": ["DB_PASSWORD"] }
```

The proposed values are stored **envelope-encrypted at rest**: the serialized
batch is encrypted under a fresh DEK that is itself wrapped by the config's
project KEK (the same hierarchy as secret values). Proposed *values* never touch
disk in plaintext and never appear in the request metadata, list views, audit
log, or API responses — only the changed **key names** are recorded.

Optionally attach a short reason with the `X-Edit-Reason` request header.

## 3. Review and approve (four-eyes)

Anyone with `secret:write` on the config can review pending requests — from the
**Approvals** screen (*Protected-config edits* section) or from the config's own
editor (*Review pending* in the protected banner). Reviews show **key names
only**, never values.

- **Approve** — decrypts the proposal and commits it as one new config version.
  The request is marked `applied` only *after* the commit succeeds, so an
  applied request always maps to a real save.

  ```
  POST /v1/configs/{cid}/edit-requests/{id}/approve
  → 200 { "version": 7, "keys": ["DB_PASSWORD"], "status": "applied" }
  ```

- **Reject** — declines the request (`POST …/reject`).
- **Cancel** — the *original requester* withdraws their own request
  (`DELETE /v1/configs/{cid}/edit-requests/{id}`).

### The four-eyes rule

The approver (and rejecter) **must be a different user than the requester**. A
self-approval returns `403`. A request that is no longer pending (already
applied, rejected, cancelled, or won by a concurrent approver) returns `409`.

## Example (fake, low-entropy values)

```bash
# protect prod, then a proposed change lands as a pending request
curl -XPUT  …/v1/configs/$PROD/require-approval  -d '{"enabled":true}'
curl -XPUT  …/v1/configs/$PROD/secrets \
     -d '{"message":"rotate","changes":[{"key":"API_TOKEN","value":"test-token-123"}]}'
# → 202 { "edit_request_id": "…", "status": "pending", "keys": ["API_TOKEN"] }

# a DIFFERENT reviewer approves → committed
curl -XPOST …/v1/configs/$PROD/edit-requests/$ID/approve
# → 200 { "version": 4, "keys": ["API_TOKEN"], "status": "applied" }
```

The values above (`test-token-123`) are deliberately fake, low-entropy
placeholders — never paste a real secret into a shell history.
