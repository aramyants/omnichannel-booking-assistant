-- Safe for a database initialized from the earlier 0001 preview migration.
CREATE INDEX IF NOT EXISTS drafts_phone ON drafts(phone,status);
CREATE UNIQUE INDEX IF NOT EXISTS one_inflight_per_phone ON drafts(phone)
 WHERE status IN ('checking','sending','uncertain');
