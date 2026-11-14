-- Postgres is an index of the repository folders on disk (the source of
-- truth). Everything here can be rebuilt by re-running the indexer.

CREATE TABLE users (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE repos (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id    bigint NOT NULL REFERENCES users (id),
    name        text NOT NULL,
    head_branch text NOT NULL DEFAULT 'main',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(), -- last time a branch moved
    indexed_at  timestamptz,
    UNIQUE (owner_id, name)
);

CREATE TABLE commits (
    repo_id         bigint NOT NULL REFERENCES repos (id) ON DELETE CASCADE,
    id              text NOT NULL CHECK (id ~ '^[0-9a-f]{40}$'),
    tree            text NOT NULL,
    parents         text[] NOT NULL DEFAULT '{}',
    author_name     text NOT NULL,
    author_email    text NOT NULL,
    authored_at     timestamptz NOT NULL,
    author_tz       smallint NOT NULL, -- minutes east of UTC
    committer_name  text NOT NULL,
    committer_email text NOT NULL,
    committed_at    timestamptz NOT NULL,
    committer_tz    smallint NOT NULL,
    message         text NOT NULL,
    summary         text GENERATED ALWAYS AS (split_part(message, E'\n', 1)) STORED,
    PRIMARY KEY (repo_id, id)
);

CREATE INDEX commits_by_date ON commits (repo_id, committed_at DESC);

CREATE TABLE refs (
    repo_id    bigint NOT NULL,
    branch     text NOT NULL,
    commit_id  text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo_id, branch),
    FOREIGN KEY (repo_id, commit_id) REFERENCES commits (repo_id, id) ON DELETE CASCADE
);
