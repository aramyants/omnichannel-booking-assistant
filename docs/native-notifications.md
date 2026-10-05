# Native booking notifications

Altegio form and administrator bookings enter `POST /webhooks/altegio` and are
queued in Cloud Tasks. The worker reads the current record with private Altegio
credentials scoped to the configured studio. It never trusts a phone or booking
details from the webhook body. Native login/verification codes are a separate
Altegio provider contract; this integration sends post-booking confirmations,
meaningful changes and cancellations.

## Current release

The business-owned AI assistant app is 2555 under developer 2830. Cloud Run pins
its partner/user credential pair to Secret Manager version 1. Old revisions keep
their prior bindings for rollback. The native notification receiver requires a
separate random capability, provided as the URL's `key` parameter. Altegio's
shared Authorization value cannot authenticate an individual destination. The
capability is never logged; a narrow Cloud Logging exclusion prevents automatic
request URL logs for this route. Sanitized application audit logs remain enabled.

Set `NATIVE_NOTIFICATIONS_ENABLED=true`, bind `ALTEGIO_WEBHOOK_SECRET`, and keep
`NATIVE_NOTIFICATIONS_ACTIVATED_AT` fixed. Use the installed app's developer
webhook settings and enable record events without replacing unrelated URLs.
Cloud Scheduler must invoke `/tasks/notifications` every five minutes with body
`{"reconcile":true}`, using the existing Cloud Tasks OIDC audience and identity.
This recovers persisted work after queue creation failures and task exhaustion.

SMS is disabled. WhatsApp is also unavailable until a valid payment setup and
approved utility templates exist. Only then set `NATIVE_WHATSAPP_READY=true` and
map `purpose:language` to actual approved template names in
`WHATSAPP_BOOKING_TEMPLATES_JSON`. Supported purposes are `booking_created`,
`booking_changed`, `booking_cancelled`; supported languages are `en`, `ru`, `hy`.
Each template has three body parameters: event title, local appointment time,
and service/specialist details. Do not enable WhatsApp merely because the phone
number or access token exists.

## Customer and staff controls

Telegram clients open `https://t.me/emotion_concept_bot?start=notifications` or
use `/notifications` and share their own contact using Telegram's contact button.
A typed phone number does not prove ownership. Only private chats with a matching
contact user ID can link. Linking expires after 180 days and can be withdrawn
with `/notifications_off`. Never merge customers solely by a phone match.

WhatsApp clients explicitly opt in using `/notifications` in a signed,
customer-initiated chat; `/notifications_off` withdraws consent. Sender identity
comes from the signed provider update. Approved template messaging is required
outside the customer service window.

The configured staff chat uses `/notification_status` and
`/notification_order telegram` or `/notification_order whatsapp`. The setting is
durable and controls the first eligible messenger. An unavailable channel is
skipped. A definitive rejection can fall back; an ambiguous timeout never causes
a duplicate retry or a message on another channel. Accepted Telegram notices
appear in the existing staff conversation inbox with updated activity time.

## Delivery guarantees and limits

Transport retries use `X-Hook-Id`; semantic duplicates are suppressed per record.
Only changes to time, service, specialist, cancellation or recipient phone cause
a notice. Comments, attendance and payments do not. Workers re-read the current
record before sending, ignore older snapshots, skip historical records on first
observation, and skip assistant bookings that already receive confirmations.
Delivery intent is durable before the provider call. A crash or timeout after
intent is recorded remains uncertain and is never automatically resent. Provider
acceptance is not proof that the client read the message.

Firestore collection `native_booking_notifications` stores private contact
links, policy, per-record version state, event work and delivery outcomes. Pending
work is bounded to batches of 100 per recovery pass. Public HTTP routes reject
missing capabilities and unauthenticated task tokens before touching work.

Validation covers concurrent duplicates, changes/cancellation, A→B→A schedule
reversions, stale/historical records, forged Telegram contacts, opt-in expiry,
channel ordering, known rejection, uncertain sends and receipt write failures.
Before claiming a customer-facing end-to-end test, link a consenting test user's
actual Telegram contact and use a clearly identified test appointment. Never seed
a real client's identity or consent to make a test pass.
