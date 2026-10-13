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

The owner subsequently authorized USB ADB on 13 October. The connected Samsung
SM-A175F reports Ucom on loaded SIM 1, LTE registration and no second SIM.
SMSGate 1.77.1 retains its SMS/phone-state permissions and battery exemption;
Cloud mode and Start on boot are enabled, and the local server remains disabled.
The app was reopened and then put in the background for a controlled delivery
check. Cloud credentials and SIM selection did not require changes.

One clearly labeled, single-part ASCII test SMS was sent through production
credentials to the studio work number itself, using an explicit device/SIM,
a unique persisted ID and a two-minute expiry. Both message and recipient
receipts returned **Delivered** for ID `fbcda5bda5064dc4b16f47c03ce7c244`.
The POST was not repeated. This verifies the tested delivery path after the
Ucom change; it does not certify delivery to every customer network or satisfy
Altegio's native provider approval requirements. No customer booking or review
request was used for the test.

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

Release **16c208c** passed CI and Cloud Build's race/emulator, vet and
vulnerability checks, and is serving 100% of production traffic. The public
health endpoint reports that version. The two pending notification events
cleared through normal processing, with zero pending rows in the final read.

The release's vulnerability scan also identified Go runtime and networking
issues published on 8 October. The module, Cloud Build test image and Docker
build image now require Go 1.27.2; `golang.org/x/net` is updated to v0.60.0
with its required dependency versions. Keep the vulnerability gate enabled.
See [Go's advisory and fixed versions](https://pkg.go.dev/vuln/GO-2026-6617).

## Authorized vendor outreach — sent and verified

The owner authorized handling the vendor correspondence. Three inquiries were
sent from **emotionconceptstudio@gmail.com** on 13 October 2026, and Gmail's
"Message sent" confirmation and the resulting conversation text were checked
for each. These are requests for eligibility, technical requirements and quotes;
no tariff change, paid-service activation or contract was accepted.

| Recipient | Sent time (Asia/Yerevan) | Request and verified conversation |
| --- | --- | --- |
| partner@alteg.io | 16:13 | [SMS aggregator eligibility and native Armenia SMS — developer 2830 / branch 1389810](https://mail.google.com/mail/u/0/?tab=rm&ogbl#sent/KtbxLrjjBDrXSgDsSpFTjZKHSCSpjkfwxV): physical-SIM eligibility, public/multitenant requirements, moderation documents/fees/timeline, production contract and sandbox, and an approved Armenian provider fallback. Referenced ticket 141720495 and requested an integration owner/ticket reference. |
| ivan.popov@messaggio.com | 16:14 | [Reply to Messaggio (WhatsApp, Viber, SMS)](https://mail.google.com/mail/u/0/?tab=rm&ogbl#search/from%3Aivan.popov%40messaggio.com+subject%3AMessaggio/FMfcgzQhWfTQdKQgCRMrkTlvnjNXCFdH): native browser OTP and review invitations, Armenian/Russian Unicode, all Armenian networks and ported numbers, SMS-only costs, eligibility, sender moderation, receipts and activation steps. The inbound sender was mailed-by and signed-by messaggio.com. |
| 444@ucom.am | 16:15 | [Automated booking SMS eligibility on Ucom studio SIM](https://mail.google.com/mail/u/0/?tab=rm&ogbl#sent/QgrcJHrtxBgWhWjLsMvRPfQCbmgqRHFfncG): permitted Android/SIM automation, transactional versus review-invitation conditions, quotas/rate limits, Unicode segment pricing, network coverage and any required corporate/A2P route while retaining the studio number. Requested referral to the business/SMS team and a ticket reference. |

The Gmail connector belongs to a different personal mailbox, so these messages
were sent through the signed-in studio Gmail UI. No gateway credentials,
customer data, original thread attachments or phone diagnostics were forwarded.
Local screenshots of the sent conversations are in
`docs/evidence/sms-outreach-2026-10-13/`; PNG files are already excluded by
`.gitignore` and are not deployment inputs.

Messaggio's official Altegio guide describes legal-entity onboarding, sender
moderation and a provider authorization key/sender code. Its Armenia OTP page
advertises a separate monthly OTP-generation product; the inquiry specifically
asks whether Altegio-generated codes can use ordinary SMS delivery without
that subscription. Published indicative operator prices are not an accepted
quote and do not establish current coverage, routing or total studio costs.

### Next implementation gates

1. Obtain Altegio's written eligibility decision and supported contract for the
   studio-owned gateway. If it is ineligible or substantially delayed, assess
   Messaggio's native SMS quote against confirmed Armenian coverage and costs.
2. Obtain Ucom's written conditions for the existing SIM delivery model. A
   successful self-test alone does not establish tariff permission.
3. Resolve the provider's eligibility, sender registration, fees and contractual
   terms before activating a paid route. Store any production credentials through
   the existing secret-management process, never in this document or Gmail replies.
4. Configure only the selected native SMS provider for branch 1389810. Validate a
   fresh browser OTP on a consenting studio test number, supported expiry and
   delivery reporting, then a controlled review request without replaying old
   customer events. Preserve the existing assistant channel integrations and
   prevent duplicate notices across native and backend delivery paths.

No native SMS activation is implied by sending these inquiries. A focused
studio-mailbox check after sending found no immediate replies from the three
contacts. Vendor eligibility and final quotes remain pending; no unattended
mailbox monitoring has been configured.

## References

- [Altegio SMS aggregator onboarding](https://alteg.io/en/support/knowledge-base/6746468657949/)
- [Messaggio native Altegio integration](https://messaggio.com/guides/integration-with-altegio/)
- [Messaggio Armenia OTP and indicative SMS pricing](https://messaggio.com/sms-verification-otp-service-provider/armenia/)
- [Ucom official support contact](https://www.ucom.am/en/help)
- [SMSGate sending and explicit SIM selection](https://docs.sms-gate.app/features/sending-messages/)
- [Backend notification configuration](native-notifications.md)
