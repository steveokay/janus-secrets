# How-to: trash, restore, and destroy

Deletion in Janus is **soft by default** and destruction is a separate,
deliberate act. All of it is value-free: the Trash shows names and ownership,
never secret material.

## Deleting

- **Project** — project board → *Delete* (modal states that all its
  environments and configs go with it)
- **Environment** — the `✕` on its column
- **Config** — *Delete* in the secret editor header
- **Single keys** — not trash: mark rows deleted in the editor and commit;
  recover by rolling back to a prior config version

Every soft delete is restorable and recorded in the audit ledger.

## Restoring

**Trash** lists deleted projects, environments, and configs with when they
were deleted. **Restore** undeletes in place — the entity returns exactly
where it was, versions and secrets intact.

## Destroying

**Destroy** is the permanent path: a hard, cascading delete of the encrypted
material (a destroyed project takes its environments, configs, and every
secret version with it). The confirmation modal is explicit because there is
no way back — the ciphertext rows are gone, and with them anything the
wrapped keys could ever decrypt.

Rule of thumb: delete freely, destroy rarely, and only after confirming
nothing references the config (secret references from other projects resolve
at read time and will start failing).
