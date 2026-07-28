-- Move "who owns this" from the individual secret to the PROJECT.
--
-- Per-key ownership was the wrong grain. A service has an owner; its individual
-- keys almost never do, so the field was either repeated on every key or left
-- empty, and neither told anyone anything. It also read confusingly next to
-- group-owned projects (migration 000047), where a binding genuinely says which
-- team owns a project. The per-key NOTE stays where it is — "read replica,
-- rotate with the primary" is real per-key information and does not generalise.
--
-- Project, not environment: ownership does not differ between a service's dev
-- and prod, so an environment-level field would be the same value copied three
-- times and would drift.
--
-- ADVISORY ONLY. This is a display label. It grants nothing, blocks nothing,
-- and is never consulted in an authorization decision — actual ownership is a
-- role binding (see groups). Do not let the two be confused.
ALTER TABLE projects
    ADD COLUMN owner text CHECK (owner IS NULL OR char_length(owner) <= 256);

-- Salvage existing per-key owners rather than destroying operator-entered data.
--
-- 1. Where every annotated key in a project agrees on ONE owner, promote it to
--    the project. That is the case this change is for, and it converts cleanly.
UPDATE projects p
   SET owner = agreed.owner
  FROM (
        SELECT e.project_id, min(a.owner) AS owner
          FROM config_secret_annotations a
          JOIN configs c      ON c.id = a.config_id
          JOIN environments e ON e.id = c.environment_id
         WHERE a.owner IS NOT NULL
         GROUP BY e.project_id
        HAVING count(DISTINCT a.owner) = 1
       ) AS agreed
 WHERE p.id = agreed.project_id;

-- 2. Where they disagree, the detail would be lost by promotion, so fold each
--    owner into that key's note instead. Nothing an operator typed is dropped.
UPDATE config_secret_annotations a
   SET note = CASE
                WHEN a.note IS NULL OR a.note = '' THEN 'owner: ' || a.owner
                ELSE a.note || E'\nowner: ' || a.owner
              END
 WHERE a.owner IS NOT NULL
   AND EXISTS (
        SELECT 1
          FROM config_secret_annotations a2
          JOIN configs c2      ON c2.id = a2.config_id
          JOIN environments e2 ON e2.id = c2.environment_id
          JOIN configs c       ON c.id = a.config_id
          JOIN environments e  ON e.id = c.environment_id
         WHERE e2.project_id = e.project_id
           AND a2.owner IS NOT NULL
           AND a2.owner <> a.owner
       );

-- Dropping the column also drops the two-column CHECK that referenced it, so
-- the "a row must carry something" guarantee is restored explicitly. Every
-- surviving row now has a note: a row could only exist with owner or note set,
-- and every owner-only row was rewritten into a note above.
ALTER TABLE config_secret_annotations DROP COLUMN owner;

DELETE FROM config_secret_annotations WHERE note IS NULL OR note = '';

ALTER TABLE config_secret_annotations
    ADD CONSTRAINT config_secret_annotations_note_present CHECK (note IS NOT NULL);
