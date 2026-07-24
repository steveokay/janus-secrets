ALTER TABLE rotation_policies DROP CONSTRAINT rotation_policies_type_check;
ALTER TABLE rotation_policies ADD CONSTRAINT rotation_policies_type_check
    CHECK (type IN ('postgres', 'webhook'));
