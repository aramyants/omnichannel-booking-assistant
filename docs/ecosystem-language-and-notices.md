# Owned booking and notification context

The Cloudflare form verifies a phone before it can publish a language preference
or appointment-message request. A narrow server-to-server bridge to the existing
Go gateway uses timestamped HMAC signatures over the exact body and path, with a
domain-separated key derived from the already shared private integration key.
Neither signing material nor private Altegio credentials enter the browser.

Firestore is the authority for a verified phone's communication language. An
explicit language choice wins over browser or messaging-app hints. The website,
linked bot conversations and transactional channel fallbacks resolve the same
preference. Unknown manual clients use Russian. Locale changes do not establish
an identity or grant permission to view appointments.

New website records use `emotion-web-v2-` IDs once the shared notification path
is enabled. Existing `omnichannel-web-` records keep their original independent
delivery ownership. The form persists the verified appointment-message request
before creation. Altegio webhooks and a post-confirmation signal use the existing
durable semantic deduplication; neither may produce a second confirmation.

Administrator bookings created from 2026-10-19T16:25:28Z qualify for appointment
notices. The fixed timestamp is a rollout boundary, not a daily moving window.
Only new, non-API staff records qualify automatically. Imported and historical
records remain excluded. Recorded client withdrawals always take precedence.
Changing a record's phone cannot transfer its prior notification permission.

Native/admin calendar capabilities are random per record and recipient. They
authorize read-only calendar details, never cancellation or client history. A
phone change rotates the capability. Validation happens before private API reads.
Every valid visit is reread before export; cancellation invalidates the calendar.
The existing Google, Apple/ICS and Outlook exports use UTC instants and show
Yerevan time. Calendar copies do not automatically sync after a change.

Messenger and SMS fallbacks keep the same resolved language. SMS can contain a
whole private calendar link within the existing bounded Unicode message budget.
An ambiguous external send is never retried or followed by another channel.

The existing five-minute recovery job and Cloud Tasks queue remain in use. No
new paid service or subscription is required by this change.
