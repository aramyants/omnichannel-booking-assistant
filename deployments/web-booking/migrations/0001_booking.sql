CREATE TABLE sessions (
 id TEXT PRIMARY KEY, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
 phone TEXT, otp_hash TEXT, otp_phone TEXT, otp_expires INTEGER,
 otp_attempts INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE TABLE drafts (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, phone TEXT NOT NULL,
 summary TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'ready',
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 appointment TEXT, management_hash TEXT, error_code TEXT
);
CREATE INDEX drafts_session ON drafts(session_id,created_at);
CREATE INDEX drafts_phone ON drafts(phone,status);
CREATE UNIQUE INDEX one_inflight_per_phone ON drafts(phone) WHERE status IN ('checking','sending','uncertain');
CREATE TABLE sms_outbox (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL, phone_hash TEXT NOT NULL,
 ip_hash TEXT NOT NULL, purpose TEXT NOT NULL, draft_id TEXT,
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, parts INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'reserved'
);
CREATE INDEX sms_time ON sms_outbox(created_at);
CREATE INDEX sms_phone_time ON sms_outbox(phone_hash,created_at);
CREATE INDEX sms_ip_time ON sms_outbox(ip_hash,created_at);
CREATE INDEX sms_session_time ON sms_outbox(session_id,created_at);
CREATE UNIQUE INDEX sms_one_confirmation ON sms_outbox(draft_id) WHERE purpose='confirmation';
CREATE TABLE rate_buckets (id TEXT PRIMARY KEY, n INTEGER NOT NULL, expires_at INTEGER NOT NULL);
CREATE INDEX rate_expiry ON rate_buckets(expires_at);
