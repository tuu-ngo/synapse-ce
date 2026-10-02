-- +goose Up
-- Generic webhooks send a class-filtered envelope by default (EPIC #1327 D5, #1367). raw_event keeps
-- the raw event body instead; it is an administrator's opt-in and needs the detail class, because the
-- raw event carries every field.
--
-- Every webhook channel that exists before this migration has always received the raw event, and
-- its receiver is built for that body. So existing webhook channels are switched to raw_event at the
-- detail class, and their receivers see no change; an administrator moves one to the envelope when
-- its receiver is ready. New channels start on the envelope.
ALTER TABLE notification_channels
    ADD COLUMN raw_event BOOLEAN NOT NULL DEFAULT false,
    ADD CONSTRAINT notification_channels_raw_event_check
        CHECK (NOT raw_event OR (channel_type = 'webhook' AND data_class = 'detail' AND NOT custom_body));

-- Migrations run as the table owner with no app.current_tenant set, and notification_channels has
-- FORCE ROW LEVEL SECURITY, so the update would silently touch nothing. Lift FORCE for this
-- transaction and restore it in the same block; a failure rolls both back (the 0185 pattern).
-- +goose StatementBegin
DO $$
BEGIN
    EXECUTE 'ALTER TABLE notification_channels NO FORCE ROW LEVEL SECURITY';
    UPDATE notification_channels SET raw_event = true, data_class = 'detail'
     WHERE channel_type = 'webhook' AND NOT custom_body;
    EXECUTE 'ALTER TABLE notification_channels FORCE ROW LEVEL SECURITY';
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE notification_channels DROP CONSTRAINT IF EXISTS notification_channels_raw_event_check;
ALTER TABLE notification_channels DROP COLUMN IF EXISTS raw_event;
