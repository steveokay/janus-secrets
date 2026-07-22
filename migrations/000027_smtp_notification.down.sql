-- Revert the type CHECK to the two-value webhook/slack set. This fails if any
-- `smtp` channel row still exists (acceptable for a down migration — remove smtp
-- channels first).
ALTER TABLE notification_channels DROP CONSTRAINT notification_channels_type_check;
ALTER TABLE notification_channels
    ADD CONSTRAINT notification_channels_type_check CHECK (type IN ('webhook','slack'));
