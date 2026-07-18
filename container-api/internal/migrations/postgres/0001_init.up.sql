CREATE TABLE IF NOT EXISTS containers (
    id         TEXT PRIMARY KEY,
    docker_id  TEXT NOT NULL,
    owner_id   TEXT NOT NULL,
    name       TEXT NOT NULL,
    image      TEXT NOT NULL,
    status     TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
