-- +goose Up
-- Whether the conversation's next turns plan rather than act (Chat's Plan
-- toggle). Stored so a restart keeps it: work queued with Plan on still plans,
-- and a live reconnect knows whether the running turn is a plan turn. Rows from
-- before this column read as Plan off, as they behaved.
ALTER TABLE conversations ADD COLUMN plan_mode BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE conversations DROP COLUMN plan_mode;
