# Altegio native SMS provider — 13 October 2026

## Confirmed support decision

Altegio's 13 October response in ticket **141720495** confirms that apps
**2554** (notifications) and **2555** (AI assistant) are active for branch
**1389810** under developer **2830**. Account recovery is complete. The ticket
was marked resolved; this does not mean native SMS was activated.

Altegio explicitly does **not** allow a private application to act as a native
SMS provider. Our own gateway must become a partner and pass Marketplace
moderation as an SMS aggregator. The partnership team at **partner@alteg.io**
supplies the full send, authentication, activation, sender and status contract.
Adding an arbitrary sender or calling an activation API is not a supported
shortcut around this requirement.

The signed-in branch notification settings still show SMS as disconnected.
The native browser cabinet needs an SMS provider connected to the branch.
Altegio sends its own verification codes in the Altegio.me mobile app; that
does not enable the browser widget. Backend SMSGate notices and the assistant's
own phone-verification flow are separate and can operate independently.

## Delivery paths

- For the shortest path to native browser verification and review SMS, evaluate
  an existing approved SMS provider. Confirm delivery to Armenian networks,
  the required review/OTP features, sender registration and total costs before
  activating an account or funding it. The branch catalogue currently lists
  Fromni SMS, InterConnect_SMS, Messaggio and SMS.to among its SMS options;
  a listing alone does not establish Armenian pricing or delivery guarantees.
- For a studio-owned or marketable provider, start the partnership process.
  Implement the bridge only against the supplied production contract and test
  expiring codes, duplicate requests, offline devices and delivery callbacks
  before enabling native messages.
- Keep the existing SMSGate backend available for eligible booking notices and
  assistant verification. Do not generate Altegio OTPs ourselves or replay old
  reviews/confirmations to test this integration.

## SIM/operator change

Changing operators while retaining the phone number does not satisfy Altegio's
partner requirement. SMSGate sends through a physical device and an explicitly
selected SIM slot; the carrier is not a request field. If the same registered
phone, SMSGate account and SIM slot remain in use, a carrier change alone does
not call for rotating cloud credentials. A different slot, reinstalled/reset
app, new device or changed credentials must be compared with the pinned
production configuration before changing it.

After porting, verify network/SMS registration, the work number, SMS credit,
SMSGate send permission, remote connection, background operation and a fresh
delivery to an explicitly consenting studio test recipient. A device-health
read does not prove SMS delivery. Obtain operator confirmation that the tariff
permits application-generated booking messages and verification codes.

Read-only checks on 13 October confirmed production build `d8f247f`, SMS enabled,
Secret Manager credential version 1, and explicit SIM slot 1. An authenticated
device-list request with the production client's user-agent returned HTTP 200
and found the pinned Samsung A17; its last check-in was 10:55:03 UTC. The cloud
API reports SIM number 1 / slot index 0 but redacts the carrier and supplies no
phone number, so it cannot establish the new operator or retained number.
A first diagnostic request using Python's default user-agent returned a
Cloudflare HTTP 403; the production-compatible probe passed. Do not interpret
that first result as an invalid credential or rotate credentials on that basis.

USB ADB detects the connected phone but currently reports `unauthorized`.
Android approval is needed before inspecting the actual SIM, app permissions
and device settings. No phone setting, cloud credential or customer delivery
was changed by these checks.

## Empty-treatment notification retry

The production backlog contained two pending events from 11 October. Their
authoritative records returned HTTP 200 with the correct branch, client,
specialist, start and duration, but an empty service list. They were staff
records, not online form submissions. The adapter's treatment-count requirement
rejected these appointments before the delivery policy could run, repeatedly
returning retryable task failures.

Native reads now accept that optional empty field while preserving required
identity, time, duration and specialist validation. Treatment IDs and names
remain empty; the existing localized renderer omits the absent treatment. SMS
and WhatsApp detail lines also omit a separator before a lone specialist.
Regression checks cover absent, null and empty treatment lists, missing required
facts, and SMS copy in all three supported languages. Normal consent, expiry,
deduplication and send-reconciliation rules remain in force. Release validation
must check that the pending backlog clears through normal task processing;
do not manually resend these events.

The release's vulnerability scan also identified Go runtime and networking
issues published on 8 October. The module, Cloud Build test image and Docker
build image now require Go 1.27.2; `golang.org/x/net` is updated to v0.60.0
with its required dependency versions. Keep the vulnerability gate enabled.
See [Go's advisory and fixed versions](https://pkg.go.dev/vuln/GO-2026-6617).

## Partnership inquiry — prepared, not sent

To: partner@alteg.io

Subject: SMS aggregator partnership for E-Motion Concept — developer 2830

Hello Altegio partnership team,

Your API team directed us to you in resolved ticket 141720495. We operate
E-Motion Concept Massage Studio, branch 1389810. Developer 2830 owns active
apps 2554 (notifications) and 2555 (AI booking assistant).

We would like to understand the requirements for registering our notification
service as an approved SMS aggregator. Our existing backend uses SMSGate on a
dedicated Android work phone and SIM for transactional studio messages, with
explicit device/SIM selection, expiry, duplicate prevention and status tracking.

Please confirm whether this delivery model is eligible, whether moderation
requires offering the service to other businesses, the required documents,
costs and expected review stages. If eligible, please provide the production
send/authentication contract, activation and sender-list requirements, delivery
status/parts/amount/currency schema, and a supported test environment for native
browser verification and review requests.

We will confirm application-generated SMS support with our mobile operator
before committing to this delivery model.

Thank you,
E-Motion Concept Massage Studio

## References

- [Altegio SMS aggregator onboarding](https://alteg.io/en/support/knowledge-base/6746468657949/)
- [SMSGate sending and explicit SIM selection](https://docs.sms-gate.app/features/sending-messages/)
- [Backend notification configuration](native-notifications.md)
