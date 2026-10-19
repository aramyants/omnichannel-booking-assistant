ALTER TABLE sms_outbox ADD COLUMN next_check_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sms_outbox ADD COLUMN receipt_until INTEGER NOT NULL DEFAULT 0;
CREATE INDEX sms_receipt_due ON sms_outbox(next_check_at,receipt_until);
CREATE UNIQUE INDEX sms_one_draft_purpose ON sms_outbox(draft_id,purpose) WHERE draft_id IS NOT NULL;
CREATE TABLE reminder_jobs (
 draft_id TEXT PRIMARY KEY, state TEXT NOT NULL DEFAULT 'pending', due_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE INDEX reminders_due ON reminder_jobs(state,due_at);
