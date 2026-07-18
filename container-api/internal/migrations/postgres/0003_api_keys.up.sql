CREATE TABLE IF NOT EXISTS api_keys (
    key_hash   TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL,
    created_at BIGINT NOT NULL
);
