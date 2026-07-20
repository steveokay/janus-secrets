# How-to: promote secrets between environments

Promotion copies selected keys from one config to another (typically dev →
staging → prod) as a first-class, reviewable operation — no manual re-typing,
no blind overwrite.

## 1. Define the pipeline

Project board → **Pipeline**. Order the environments with the arrows and
*Save pipeline*. Promotions flow left → right; the editor's *Promote →*
targets the next stage. Environments created after the pipeline was saved are
appended automatically — reorder and save to place them.

## 2. Stage a promotion

Two entry points, same review:

- **Drag and drop** — on the project board, drag a config tile onto another
  environment's column (valid targets show a dashed outline). Works for any
  target, including backwards or across the pipeline.
- **Promote →** in the secret editor — targets the next pipeline stage (a
  dropdown appears when several stages follow).

Either way you get the **review panel**: every key with its change status
(`+ add`, `~ change`, `− remove`, `same`), source → target values side by
side, and a checkbox per key (adds/changes pre-selected). If the target env
has no root config, the promotion **creates** one. Nothing applies until you
act.

## 3. Apply, or ask for approval

- **Apply now** — commits the selected keys as one new version on the target.
- **Request approval** — files a promotion request with a note instead.
  Requests appear on **Approvals**, where a *different* member reviews a
  value-free diff and approves (which applies it), rejects with a note, or
  the requester cancels. The server enforces four-eyes: **you cannot approve
  your own request**.

## Locked keys

In the target config's editor, **Lock** any key that promotions must never
overwrite (`⚿` appears next to it). Locked keys show as disabled rows in
every promotion diff. Typical use: prod-only values like `DATABASE_URL` that
must not be clobbered by whatever dev has.

## Notes

- A promotion is an ordinary immutable version on the target config —
  visible in its history, diffable, and rollback-able like any save.
- Rollback never rewrites: it commits a new version identical to the target
  version.
- The environment **slug** is the identity used by the pipeline, the CLI, and
  secret references; *Rename* on the board changes only the display name.
