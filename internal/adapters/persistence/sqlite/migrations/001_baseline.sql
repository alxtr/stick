CREATE TABLE schema_migrations (
    version    INTEGER PRIMARY KEY CHECK (version > 0),
    name       TEXT NOT NULL,
    applied_at DATETIME NOT NULL
);

CREATE TABLE sticks (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    version      INTEGER NOT NULL DEFAULT 1 CHECK (typeof(version) = 'integer' AND version > 0),
    holder_sub   TEXT,
    holder_name  TEXT,
    holder_email TEXT,
    claimed_at   DATETIME,
    reason       TEXT,
    archived_at  DATETIME,
    CHECK ((holder_sub IS NULL AND holder_name IS NULL AND holder_email IS NULL AND claimed_at IS NULL AND reason IS NULL)
        OR (holder_sub IS NOT NULL AND holder_name IS NOT NULL AND holder_email IS NOT NULL AND claimed_at IS NOT NULL AND reason IS NOT NULL)),
    CHECK (archived_at IS NULL OR holder_sub IS NULL)
);

CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    stick_id     TEXT NOT NULL REFERENCES sticks(id),
    holder_sub   TEXT NOT NULL,
    holder_name  TEXT NOT NULL,
    holder_email TEXT NOT NULL,
    reason       TEXT NOT NULL,
    claimed_at   DATETIME NOT NULL,
    released_at  DATETIME
);
CREATE INDEX idx_sessions_stick ON sessions(stick_id);
CREATE UNIQUE INDEX idx_sessions_one_active_per_stick ON sessions(stick_id) WHERE released_at IS NULL;
CREATE INDEX idx_sessions_completed_history ON sessions(stick_id, claimed_at DESC) WHERE released_at IS NOT NULL;

CREATE TABLE subscriptions (
    stick_id         TEXT NOT NULL REFERENCES sticks(id),
    user_sub         TEXT NOT NULL,
    user_name        TEXT NOT NULL,
    user_email       TEXT NOT NULL,
    generation_token TEXT NOT NULL UNIQUE CHECK (length(generation_token) > 0),
    PRIMARY KEY (stick_id, user_sub)
);
CREATE INDEX idx_subscriptions_user_stick ON subscriptions(user_sub, stick_id);

CREATE TABLE notification_outbox (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    stick_id                TEXT NOT NULL REFERENCES sticks(id),
    stick_name              TEXT NOT NULL,
    holder_name             TEXT NOT NULL,
    holder_email            TEXT NOT NULL,
    held_since               DATETIME NOT NULL,
    released_at             DATETIME NOT NULL,
    recipient_sub           TEXT NOT NULL,
    recipient_name          TEXT NOT NULL,
    recipient_email         TEXT NOT NULL,
    subscription_generation_token TEXT NOT NULL CHECK (length(subscription_generation_token) > 0),
    status                  TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'failed', 'succeeded')),
    attempts                INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at         DATETIME,
    claim_token             TEXT,
    claimed_at              DATETIME,
    last_attempt_at         DATETIME,
    delivered_at            DATETIME,
    last_error              TEXT,
    created_at              DATETIME NOT NULL,
    CHECK ((status = 'in_progress' AND claim_token IS NOT NULL AND claimed_at IS NOT NULL)
        OR (status <> 'in_progress' AND claim_token IS NULL AND claimed_at IS NULL)),
    CHECK (delivered_at IS NULL OR status = 'succeeded')
);
CREATE INDEX idx_notification_outbox_ready ON notification_outbox(status, next_attempt_at, id);
CREATE INDEX idx_notification_outbox_stale ON notification_outbox(status, claimed_at, id);
