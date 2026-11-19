-- +goose Up
ALTER TABLE conversation_turns ADD COLUMN provider_delivery_state TEXT NOT NULL DEFAULT 'none'
    CHECK (provider_delivery_state IN ('none', 'attempted', 'accepted'));

-- Existing turns cannot reconstruct the exact boundary: deferred turns were
-- bound and marked running before ACP start. Completion proves acceptance;
-- any other old provider evidence stays ambiguous.
UPDATE conversation_turns
SET provider_delivery_state = 'accepted'
WHERE provider_turn_id <> '' AND state = 'completed';

UPDATE conversation_turns
SET provider_delivery_state = 'attempted'
WHERE provider_delivery_state = 'none'
  AND (provider_turn_id <> '' OR provider_input_text <> '');

-- +goose Down
ALTER TABLE conversation_turns DROP COLUMN provider_delivery_state;
