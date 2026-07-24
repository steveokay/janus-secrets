-- Relax the rotation_policies.type CHECK to admit the rotator types added after
-- migration 000010 (which only allowed 'postgres'/'webhook'). Without this the
-- DB layer rejected creating mysql/redis rotators (shipped earlier but blocked
-- here) as well as the new oauth/aws_iam rotators. The 000010 CHECK was inline
-- and unnamed, so Postgres auto-named it rotation_policies_type_check.
ALTER TABLE rotation_policies DROP CONSTRAINT rotation_policies_type_check;
ALTER TABLE rotation_policies ADD CONSTRAINT rotation_policies_type_check
    CHECK (type IN ('postgres', 'webhook', 'mysql', 'redis', 'oauth', 'aws_iam'));
