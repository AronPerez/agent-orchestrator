-- +goose Up
CREATE TABLE session_overlap_notices (
    session_id      TEXT      NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    peer_session_id TEXT      NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    notified_at     TIMESTAMP NOT NULL,
    PRIMARY KEY (session_id, peer_session_id),
    CHECK (session_id <> peer_session_id)
);

CREATE INDEX idx_session_overlap_notices_peer
    ON session_overlap_notices(peer_session_id);

-- +goose Down
DROP INDEX idx_session_overlap_notices_peer;
DROP TABLE session_overlap_notices;
