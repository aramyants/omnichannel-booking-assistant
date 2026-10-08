# Production appointment and confirmation incident — 8 October 2026

## Confirmed causes

The customer's 16:00 appointment existed in Altegio. Both WhatsApp appointment
queries failed in build `cee2bf6`; Cloud Logging recorded `scheduling system
unavailable: appointment phone mismatch` at 15:27 Yerevan. The client-search and
record-list APIs returned different phone formats. Bare international digits
from the record API were passed through the Armenian national-input parser.
Armenian-only fixtures had hidden this defect. Provider adaptation now canonicalizes
valid international digits while preserving exact owner/location checks and
strict customer-input validation. Signed Meta delivery receipts require the same
international adaptation.

The missing booking confirmation had a separate cause. The creation webhook
arrived on 6 October at 18:46 Yerevan; the durable delivery ledger recorded an
unavailable studio Telegram route followed by `accepted_sms`. Read-only SMSGate
inspection established terminal `Failed` with recipient error `TTL expired`.
The SMS was queued but did not complete within its 15-minute validity period.
The provider response does not establish why the work device failed to send it.
The backend had never reconciled final SMS status, leaving acceptance visible
without the subsequent failure. No manual customer message or booking mutation
was performed during diagnosis.

## Changes

- Read-only SMS status reconciliation uses the original stable provider ID,
  durable leases and paced oldest-first checks. Failures alert staff once;
  missing receipts remain unconfirmed. No unknown send or old confirmation is
  replayed. Multipart delivered status does not prove every part arrived.
- Native queue recovery proceeds independently of WhatsApp health checks and
  continues after an individual enqueue failure. Confirmed WhatsApp failures
  persist fallback before enqueue; later delivery evidence cancels unsent fallback.
- Delayed confirmations and changes expire when their appointment begins.
- Active delivery and reminder leases return retryable HTTP failures; completed
  work acknowledges normally. Failed turn registration releases its lease.
- Firestore reminder ownership is reset on every transaction callback attempt.
  A real SDK conflict test reproduces the losing-worker retry without an emulator.
- HTTP responses support the two-minute turn budget. Shutdown cancels provider
  work and allows cleanup inside Cloud Run's ten-second termination window.
- GitHub CI and Cloud Build now start the official Firestore emulator before
  their race suites, rather than silently skipping storage integration tests.

## Operational requirements and limits

SMS delivery requires the commissioned phone to stay powered, connected, able
to run SMSGate in the background, and funded for carrier SMS. A software flag
cannot establish those conditions. Queued, SMSC accepted, and recipient-delivered
are distinct outcomes. The monitor observes for 24 hours and retains sanitized
provider state; it does not guarantee delivery during a device/carrier outage.

Provision the SMS receipt composite index with
`deployments/gcp/ensure-sms-index.sh` before release. Existing consent cutoffs,
withdrawal, exact contact ownership, and uncertain-send protections remain in
force. Native Altegio login SMS still depends on the separate provider contract.

The fixes reduce concrete loss/duplicate paths; they cannot guarantee zero
external outages. Assistant reply completion can still become ambiguous if the
external send succeeds and durable completion fails. Broad exactly-once delivery
would require provider-supported idempotency and outbound reconciliation.
