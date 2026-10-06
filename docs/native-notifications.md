# Native booking notifications

Altegio form and administrator bookings enter `POST /webhooks/altegio` and are
queued in Cloud Tasks. The worker reads the current record with private Altegio
credentials scoped to the studio. Webhook bodies cannot supply recipients or
appointment details. This integration sends confirmations, meaningful changes
and cancellations. Altegio login/verification codes remain a separate provider
contract. Booking SMS fallback uses the commissioned work Android phone.

## Customer flow and sender identity

The native form explains that submitting a booking requests E-Motion appointment
updates through Telegram or WhatsApp, with SMS as a fallback. No bot command or separate contact-sharing
step is required for this flow. The on-screen booking result remains available.

Default order is Telegram, then WhatsApp, then SMS. A valid verified bot link keeps delivery
from `@emotion_concept_bot`. Otherwise, an eligible booking request can use the
authorized studio user account `@emotion_concept` to resolve the customer's phone
through Telegram and send the same appointment copy. User-account notices include
ordinary links because bot inline keyboards cannot be sent by user accounts.

Telegram's phone lookup is privacy-limited: an unavailable result does not prove
that the client has no Telegram account. The adapter does not import contacts,
enumerate account histories, bypass recipient restrictions, or pay for contact.
It reserves one lookup per three seconds across instances and honors flood-wait
cooldowns. A definitively unavailable Telegram route can fall back to WhatsApp.
An ambiguous send outcome never triggers another channel or automatic resend.

## Work phone SMS fallback

SMSGate 1.77.1 is installed from its verified official secure APK on the studio's
Samsung Galaxy A17, with work SIM +37494768067 in slot 1. Its cloud credentials
are pinned in Secret Manager as `smsgate-work-phone-credentials:1` and supplied
through `SMSGATE_CREDENTIALS_JSON`. No Android LAN endpoint is exposed. Start on
boot and the battery-optimization exemption are enabled. A real commissioning
SMS reported **Delivered** after the studio refilled the SIM balance.

`NATIVE_SMS_READY=true` enables the final route only for current client requests.
`NATIVE_SMS_PERMISSION_SINCE=2026-10-06T09:40:06Z` is the fixed SMS disclosure
cutoff. Earlier form bookings are not enrolled into SMS. Staff can separately
record a client's booking-update request with `/notification_allow`; a global
`/notification_block` withdrawal wins over the form request.

SMS contains the local date/time, treatment, specialist, address and callback
number, capped at 201 UTF-16 units (at most three Unicode SMS parts). Each send
pins the work device and SIM, uses a stable 32-character provider ID, requests
delivery reports, and expires after 15 minutes or at the appointment start,
whichever comes first. No uncertain send is retried. `accepted_sms` means queued
by SMSGate, not received by the client; provider status can be queried by the
stable ID. Keep the phone powered, online, and supplied with carrier SMS credit.

WhatsApp native notices carry a client-data-free native event reference. The
signed webhook for the configured business number records sent/delivered/read/
failed receipts. A confirmed failure requeues the original event, skips routes
already attempted and proceeds to the remaining route. It rechecks the booking
and withdrawal before sending. Unknown outcomes do not trigger SMS. Read or
delivered receipts cannot be overwritten by an older failure.

Incoming SMS stays in the phone's normal Messages app. This release does not
turn on an SMS AI assistant or connect Altegio's separate login/OTP provider.

Studio-account replies appear in the studio's normal Telegram app. They are not
imported into the bot inbox and do not receive AI replies through this adapter.
The existing bot conversations and staff inbox remain separate. The shared AI
assistant continues to handle customer-initiated WhatsApp conversations.

## Booking request and withdrawal

`NATIVE_BOOKING_PERMISSION_SINCE` is the fixed time the booking-update request
became visible in the public form: `2026-10-05T19:21:45Z`. The worker records that
request only on the first observed `create` event for a new `online=true` record
with an empty external API ID, created after that timestamp. This is an inference
from native form submission with the printed request, not a separately captured
checkbox. Never apply it retroactively to imported, historical or staff bookings.
The request stays bound to the original phone; editing the booking phone cannot
transfer permission to another person.

For a staff-created booking, the configured staff chat can record a client's
documented request once with `/notification_allow +374… hy|ru|en`. This request
expires after 180 days and does not establish a verified bot identity. Staff must
use it only after the client has requested appointment messages. It is not an AI
tool and ordinary clients cannot invoke it. `/notification_block +374…` withdraws
all automatic notification routes. Staff should use it when a client asks the
studio account to stop. The account adapter does not read incoming STOP messages.
Standalone STOP in the signed WhatsApp channel withdraws all automatic routes.

Optional bot linking remains available through
`https://t.me/emotion_concept_bot?start=notifications` or `/notifications`.
Only the contact button with a matching Telegram user ID proves the association;
a typed phone does not. Links expire after 180 days. `/notifications_off` withdraws
that channel and a new online booking cannot silently override the withdrawal.
WhatsApp `/notifications en|ru` preferences also remain available, with
`/notifications_off` for channel withdrawal. Explicit renewal can restore a
withdrawn channel.

`/notification_status` shows the policy and account readiness flag.
`/notification_order telegram` or `/notification_order whatsapp` changes the first
eligible messenger. Native Telegram notices default to Armenian unless a saved
bot or staff preference exists. WhatsApp uses its own saved template language,
defaulting to English. There is no approved Armenian template in this release.

## Account commissioning and release

Altegio AI assistant app 2555 belongs to developer 2830 under the owner's current
account. Cloud Run pins its partner/user credential pair to Secret Manager version
1. Previous revisions retain their original bindings for rollback.

Create the private Telegram application in the studio's own developer portal.
Run `cmd/telegram-connect` locally with a private config path and private session
output path. The studio owner scans its loopback QR in Telegram Settings → Devices.
The tool verifies the exact phone, username and non-bot identity before saving.
The session grants Telegram account access and can be revoked in Devices; the
implemented worker uses it only for transactional appointment notices.

Store the session encrypted with AES-256-GCM in
`native_booking_notifications/telegram_studio_account`. Bind authenticated data
to the studio phone. Store the 32-byte encryption key and private application
credentials in Secret Manager, never in the repository or logs. The release binds
`TELEGRAM_ACCOUNT_CREDENTIALS_JSON=telegram-studio-notifications-credentials:1`
and enables `NATIVE_TELEGRAM_ACCOUNT_READY=true`. A durable account lease prevents
simultaneous use of its authorization key by multiple Cloud Run instances;
expired workers cannot overwrite newer session state.

`cmd/telegram-check` verifies the encrypted cloud session and exact studio identity
without resolving a customer phone, reading chat history or sending messages.
Disabling the readiness flag turns off studio-account delivery while preserving
existing bot delivery. Revoking the session makes this route unavailable and
permits an otherwise eligible WhatsApp fallback.

The owner completed WhatsApp billing. On 6 October 2026, after the approved
work-phone reconnection and a real client cabinet check, Meta approved both
styled utility templates: English `emotion_booking_update` and Russian
`emotion_booking_update_ru`. Deployment preserves both the runtime readiness
flag and approved language mapping. Enable only approved `purpose:language` entries in
`WHATSAPP_BOOKING_TEMPLATES_JSON`. Supported purposes are `booking_created`,
`booking_changed`, `booking_cancelled`; each template receives three parameters:
status, local date/time, and service/specialist. General appointment messaging
permission is still required. Native booking requests can supply it as above;
merely possessing a phone number cannot.

The receiver requires a separate random capability in the URL's `key` parameter.
Altegio's shared Authorization value does not authenticate an individual receiver.
The capability is never logged; a narrow Cloud Logging exclusion removes automatic
request URL logs for this route. Sanitized application audit logs remain enabled.
Keep `NATIVE_NOTIFICATIONS_ACTIVATED_AT` fixed. Cloud Scheduler invokes the
authenticated `/tasks/notifications` recovery action every five minutes.

## Delivery guarantees and verification

Transport retries use `X-Hook-Id`; semantic duplicates are suppressed per record.
Only time, service, specialist, cancellation or recipient changes produce notices.
Workers re-read immediately before sending, ignore older snapshots, skip historical
records on first observation, and skip assistant bookings with existing confirmations.
Delivery intent is durable before a provider call. A crash or timeout after intent
remains uncertain and is never automatically resent. Acceptance is not proof of
delivery or reading. Account outcomes are identified as `telegram_account`;
bot outcomes retain `telegram`. Accepted bot notices appear in its staff inbox.

Validation covers duplicates, schedule reversions, stale records, forged contacts,
permission cutoff/source/phone changes, channel withdrawals, authenticated staff
controls, encryption identity binding and lease fencing. The commissioning check
verifies the actual cloud session, but it does not test customer delivery. Before
claiming a live end-to-end test, obtain an explicit consenting test recipient and
use a clearly identified test message or booking. Never seed a real client's
identity or permission just to make a test pass.
