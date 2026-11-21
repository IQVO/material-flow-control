-- material-flow-control schema (ADR 0002). Aggregates are stored as JSONB snapshots; the other columns exist for lookups.

-- flow_lock: the one row every write unit of work locks first (decision D19).
CREATE TABLE flow_lock (
    id SMALLINT PRIMARY KEY CHECK (id = 1)
);
INSERT INTO flow_lock (id) VALUES (1);

-- transport_requests: the ONE outcome of every transport_request_id (responsibility 6).
CREATE TABLE transport_requests (
    transport_request_id TEXT COLLATE "C" PRIMARY KEY,
    container_id         TEXT        NOT NULL,
    outcome              TEXT        NOT NULL CHECK (outcome IN ('ACCEPTED', 'REJECTED')),
    transport_order_id   TEXT,
    record               JSONB       NOT NULL,
    recorded_at          TIMESTAMPTZ NOT NULL
);

CREATE TABLE transport_orders (
    transport_order_id   TEXT COLLATE "C" PRIMARY KEY,
    transport_request_id TEXT,
    container_id         TEXT        NOT NULL,
    status               TEXT        NOT NULL,
    internal             BOOLEAN     NOT NULL,
    snapshot             JSONB       NOT NULL,
    version              BIGINT      NOT NULL,
    accepted_at          TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_transport_orders_open ON transport_orders (accepted_at, transport_order_id) WHERE status = 'IN_PROGRESS';

-- transport_legs: leg reference -> order, the key every subsystem fact is matched by (ADR 0002 decision 3).
CREATE TABLE transport_legs (
    leg_ref            TEXT COLLATE "C" PRIMARY KEY,
    transport_order_id TEXT NOT NULL REFERENCES transport_orders (transport_order_id) ON DELETE CASCADE
);

CREATE TABLE container_tracks (
    container_id     TEXT COLLATE "C" PRIMARY KEY,
    position_kind    TEXT        NOT NULL,
    position_code    TEXT        NOT NULL DEFAULT '',
    active_order_id  TEXT        NOT NULL DEFAULT '',
    in_clarification BOOLEAN     NOT NULL,
    snapshot         JSONB       NOT NULL,
    version          BIGINT      NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_container_tracks_clarification ON container_tracks (container_id) WHERE in_clarification;

CREATE TABLE container_moves (
    id           BIGSERIAL   PRIMARY KEY,
    container_id TEXT        NOT NULL,
    move         JSONB       NOT NULL,
    moved_at     TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_container_moves_container ON container_moves (container_id, id DESC);

CREATE TABLE station_queues (
    station_id TEXT COLLATE "C" PRIMARY KEY,
    snapshot   JSONB  NOT NULL,
    version    BIGINT NOT NULL
);

CREATE TABLE subsystem_capacities (
    kind         TEXT        NOT NULL,
    subsystem_id TEXT        NOT NULL,
    snapshot     JSONB       NOT NULL,
    measured_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (kind, subsystem_id)
);

CREATE TABLE equipment_units (
    asrs_id TEXT  NOT NULL,
    unit_id TEXT  NOT NULL,
    state   JSONB NOT NULL,
    PRIMARY KEY (asrs_id, unit_id)
);

-- subsystem_commands: the command outbox (ADR 0002 decision 4).
CREATE TABLE subsystem_commands (
    seq                BIGSERIAL   PRIMARY KEY,
    command_id         TEXT        NOT NULL UNIQUE,
    transport_order_id TEXT        NOT NULL,
    leg_ref            TEXT        NOT NULL,
    kind               TEXT        NOT NULL CHECK (kind IN ('REQUEST_LEG', 'CANCEL_LEG', 'CONFIRM_PICKUP')),
    idempotency_key    TEXT        NOT NULL,
    status             TEXT        NOT NULL CHECK (status IN ('PENDING', 'DONE', 'FAILED')),
    attempts           INTEGER     NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL,
    next_attempt_at    TIMESTAMPTZ NOT NULL,
    deadline           TIMESTAMPTZ NOT NULL,
    last_error         TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX idx_subsystem_commands_due ON subsystem_commands (next_attempt_at, seq) WHERE status = 'PENDING';

-- processed_events: the consumers' inbox, keyed by the CloudEvents id (checklist items 3-4).
CREATE TABLE processed_events (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_processed_events_at ON processed_events (processed_at);

-- outbox_events: the fleet's transactional outbox shape.
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- idempotency_keys: stored answers of POSTs (2xx, FINAL and OUTCOME 4xx only; never RETRYABLE, never 5xx).
CREATE TABLE idempotency_keys (
    key          TEXT PRIMARY KEY,
    fingerprint  TEXT        NOT NULL,
    status       INTEGER     NOT NULL CHECK (status >= 200 AND status < 500),
    content_type TEXT        NOT NULL DEFAULT '',
    body         BYTEA       NOT NULL DEFAULT ''::bytea,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
