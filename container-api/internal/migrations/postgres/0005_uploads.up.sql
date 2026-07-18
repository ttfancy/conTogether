CREATE TABLE IF NOT EXISTS uploads (
    id           TEXT PRIMARY KEY,
    owner_id     TEXT NOT NULL,
    filename     TEXT NOT NULL,
    path         TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size         BIGINT NOT NULL,
    visibility   TEXT NOT NULL DEFAULT 'private',
    created_at   BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_uploads_owner_id ON uploads (owner_id);
