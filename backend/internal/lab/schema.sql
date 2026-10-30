CREATE TABLE users (
 id uuid PRIMARY KEY,
 email text NOT NULL UNIQUE,
 name text NOT NULL,
 password_hash text NOT NULL
);
CREATE TABLE sessions (
 digest char(64) PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id),
 csrf_token text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE login_throttle (
 address_digest char(64) PRIMARY KEY,
 failures integer NOT NULL DEFAULT 0,
 window_start timestamptz NOT NULL DEFAULT now(),
 blocked_until timestamptz
);
CREATE TABLE workspace_guard (id integer PRIMARY KEY CHECK(id=1));
INSERT INTO workspace_guard VALUES (1);
CREATE TABLE queue_control (
 id integer PRIMARY KEY CHECK(id=1),
 paused boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 0 CHECK(version>=0)
);
INSERT INTO queue_control(id) VALUES (1);
CREATE TABLE destinations (
 id text PRIMARY KEY CHECK(id IN ('warehouse','billing')),
 name text NOT NULL,
 route text NOT NULL,
 mode text NOT NULL DEFAULT 'HEALTHY' CHECK(mode IN ('HEALTHY','FAIL','TIMEOUT')),
 version bigint NOT NULL DEFAULT 0 CHECK(version>=0)
);
INSERT INTO destinations(id,name,route) VALUES
 ('warehouse','Warehouse demo','/hooks/warehouse'),
 ('billing','Billing demo','/hooks/billing');
CREATE TABLE events (
 id uuid PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 destination_id text NOT NULL REFERENCES destinations(id),
 event_type text NOT NULL CHECK(event_type IN ('order.created','order.cancelled')),
 payload text NOT NULL,
 envelope bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE deliveries (
 event_id uuid PRIMARY KEY REFERENCES events(id),
 state text NOT NULL CHECK(state IN ('QUEUED','SENDING','RETRY_WAIT','DELIVERED','DEAD_LETTER')),
 version bigint NOT NULL DEFAULT 0 CHECK(version>=0),
 cycle integer NOT NULL DEFAULT 1 CHECK(cycle BETWEEN 1 AND 3),
 cycle_attempts integer NOT NULL DEFAULT 0 CHECK(cycle_attempts BETWEEN 0 AND 3),
 total_attempts integer NOT NULL DEFAULT 0 CHECK(total_attempts BETWEEN 0 AND 9),
 redrives integer NOT NULL DEFAULT 0 CHECK(redrives BETWEEN 0 AND 2),
 next_attempt_at timestamptz,
 lease_token uuid,
 lease_expires_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK(cycle=redrives+1),
 CHECK((state='SENDING' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (state<>'SENDING' AND lease_token IS NULL AND lease_expires_at IS NULL))
);
CREATE INDEX deliveries_ready ON deliveries(next_attempt_at,event_id) WHERE state IN ('QUEUED','RETRY_WAIT');
CREATE INDEX deliveries_expired ON deliveries(lease_expires_at) WHERE state='SENDING';
CREATE TABLE idempotency_keys (
 actor_id uuid NOT NULL REFERENCES users(id),
 key uuid NOT NULL,
 fingerprint char(64) NOT NULL,
 event_id uuid NOT NULL UNIQUE REFERENCES events(id),
 PRIMARY KEY(actor_id,key)
);
CREATE TABLE attempts (
 id uuid PRIMARY KEY,
 event_id uuid NOT NULL REFERENCES events(id),
 number integer NOT NULL CHECK(number BETWEEN 1 AND 9),
 cycle integer NOT NULL CHECK(cycle BETWEEN 1 AND 3),
 status text NOT NULL CHECK(status IN ('STARTED','SUCCEEDED','HTTP_ERROR','TIMEOUT','NETWORK_ERROR','ABANDONED')),
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 http_status integer CHECK(http_status BETWEEN 100 AND 599),
 duration_ms bigint CHECK(duration_ms>=0),
 response_preview text CHECK(octet_length(response_preview)<=2048),
 error_category text CHECK(error_category IN ('HTTP_STATUS','TIMEOUT','NETWORK_ERROR','OUTCOME_UNKNOWN')),
 worker_id text NOT NULL,
 UNIQUE(event_id,number),
 CHECK((status='STARTED' AND finished_at IS NULL) OR (status<>'STARTED' AND finished_at IS NOT NULL))
);
CREATE UNIQUE INDEX one_started_attempt ON attempts(event_id) WHERE status='STARTED';
CREATE TABLE receipts (
 destination_id text NOT NULL REFERENCES destinations(id),
 event_id uuid NOT NULL,
 first_received_at timestamptz NOT NULL DEFAULT now(),
 last_received_at timestamptz NOT NULL DEFAULT now(),
 receipt_count integer NOT NULL DEFAULT 1 CHECK(receipt_count>=1),
 effect_count integer NOT NULL DEFAULT 1 CHECK(effect_count=1),
 body_sha256 char(64) NOT NULL,
 PRIMARY KEY(destination_id,event_id)
);
CREATE TABLE workers (
 id text PRIMARY KEY,
 last_seen_at timestamptz NOT NULL
);
CREATE TABLE receiver_effects (
 destination_id text NOT NULL,
 event_id uuid NOT NULL,
 applied_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(destination_id,event_id),
 FOREIGN KEY(destination_id,event_id) REFERENCES receipts(destination_id,event_id)
);
CREATE TABLE audit (
 id uuid PRIMARY KEY,
 created_at timestamptz NOT NULL DEFAULT now(),
 actor text NOT NULL,
 action text NOT NULL,
 event_id uuid REFERENCES events(id),
 note text NOT NULL
);
CREATE INDEX audit_order ON audit(created_at DESC,id DESC);
CREATE INDEX audit_event_order ON audit(event_id,created_at DESC,id DESC);
