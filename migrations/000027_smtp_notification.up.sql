-- SMTP notification channel: add `smtp` as a third channel type. Rotation/sync
-- failures, denials, and pending approvals can now be delivered by email as well
-- as to webhook / Slack. The SMTP settings (host/port/from/to/username/password/
-- tls_mode/insecure_skip_verify) live inside the existing opaque, master-wrapped
-- config_ct blob — no other schema change.

-- The type CHECK constraint is inline+unnamed in 000024, so Postgres auto-named
-- it `notification_channels_type_check`. Drop and re-add it with `smtp` allowed.
ALTER TABLE notification_channels DROP CONSTRAINT notification_channels_type_check;
ALTER TABLE notification_channels
    ADD CONSTRAINT notification_channels_type_check CHECK (type IN ('webhook','slack','smtp'));
