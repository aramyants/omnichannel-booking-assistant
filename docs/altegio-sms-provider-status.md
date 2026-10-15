# Altegio native SMS provider — checked 15 October 2026

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

No native SMS activation is implied by sending these inquiries. The initial
13 October mailbox check found no immediate replies. Subsequent replies and
follow-up actions are recorded below; no unattended mailbox monitoring has
been configured.

## 15 October: replies, verification blocker and follow-up

The studio mailbox was checked across Altegio, Messaggio, Ucom and mailer-daemon
messages since 12 October. No delivery-failure message appeared in that search.

- Ucom replied on 13 October at 23:58 that it does not currently offer an
  organizational bulk-SMS service. This does **not** answer whether ordinary
  SMS automation on the existing Android SIM is allowed, and must not be
  treated as either permission or a ban on that model. A verified reply was
  sent in the same thread on 15 October, explicitly asking the SIM/tariff
  question, transactional versus review conditions and a business-team referral.
- Messaggio's Ivan Popov replied on 13, 14 and 15 October with the same request
  to start through WhatsApp or Telegram. No eligibility decision, quote or
  activation requirements were supplied. Studio-profile Telegram Web is signed
  out; no message was sent there. No Android device was attached to ADB during
  this audit. The Messaggio portal is also signed out and its continuation text
  accepts contractual terms; no account, contract or paid route was activated.
- Altegio's partnership inquiry has no reply. A verified follow-up was sent in
  that thread on 15 October, requesting a named integration owner, yes/no
  eligibility decision, supported contract or an approved Armenian alternative,
  ticket reference and expected response date.
- A new [Messaggio team inquiry](https://mail.google.com/mail/u/0/?tab=rm&ogbl#sent/KtbxLwglqKSzBjbcNLGDKpqHbTCTRJGmwg)
  was sent and verified at 11:52 on 15 October to info@messaggio.com,
  support@messaggio.com and ivan.popov@messaggio.com. The two team addresses
  were confirmed on Messaggio's official contact page. It requests onboarding
  by email and a named owner, with native OTP/review coverage, legal-entity
  eligibility, full SMS-only costs, sender registration and activation steps.
- A second approved-provider option was contacted through the address in
  SMS.to's official Altegio integration guide: support@sms.to. The verified
  [SMS.to inquiry](https://mail.google.com/mail/u/0/?tab=rm&ogbl#sent/KtbxLwHDghdCmlLsRWwVWzgswgmDdMcHSV)
  was sent at 11:53 on 15 October for Armenian network/ported-number coverage,
  native browser OTP/review delivery, sender rules, tests, full costs and
  qualification/activation requirements. No spending or contract was committed.

### Native web phone verification is disabled by Altegio

The signed-in branch client-field settings at
`/online_booking/1389810/settings/client_fields` show the checkbox
**"Подтверждать номер клиента по SMS"** (confirm the client's phone by SMS)
unchecked and disabled. The adjacent notice states that the settings are
available when an SMS aggregator is connected. The phone field is mandatory,
but mandatory entry is not proof of ownership. Push-notification verification
is checked separately and does not enable browser SMS verification.

The private app's SMS sender remains **"Не выбрано"** (not selected). These
controls establish the current native blocker; no disabled controls, arbitrary
sender values or undocumented activation calls were used to bypass it.

The shortest supported route is a qualified existing provider and usable sender,
followed by enabling this SMS-verification setting and verifying a fresh code on
a consenting studio test number. Native review SMS also needs the usable route.
The studio-owned aggregator partnership remains a separate, longer-term option.

### Assistant verification is separate from first-booking verification

Production still serves revision `omnichannel-booking-assistant-00102-but` at
100%, and `/health` returns version `16c208c`. `NATIVE_NOTIFICATIONS_ENABLED`
and `NATIVE_SMS_READY` are true. Both `SMSGATE_CREDENTIALS_JSON` and
`ALTEGIO_WEBHOOK_SECRET` use pinned Secret Manager version 1. An authenticated
SMSGate device read matched the configured phone and explicit SIM 1, with a
reported check-in of `2026-10-15T10:32:03+03:00`. No fresh customer SMS or
native OTP was sent in this audit; this read does not replace the 13 October
controlled delivery evidence.

The assistant `/verify` flow protects appointment-history access and
management. It uses expiring, single-use six-digit codes bound to channel and
account, persisted phone/account budgets and attempt limits, HMAC-only storage,
and transcript redaction. WhatsApp's authenticated address and Telegram's own
contact are also accepted ownership proofs. A typed booking contact alone is
not verification. The audit found that the prior `prepare_booking` and
`confirm_booking` creation flow did not require this proof.

The recommended verify-once policy was selected after presenting the scope
question and receiving no alternative during the audit. The new assistant
creation gate checks proof for the **exact booking phone** both when preparing
and confirming, including drafts from older releases and alternate WhatsApp
booking numbers. It saves the validated proposal, offers its existing phone as
a one-tap SMS request (or Telegram own-contact verification), and sends no SMS
until the customer requests it. Successful matching proof restores the service,
duration, price, specialist, time, name and phone summary, then asks for a fresh
booking confirmation. A code is never treated as booking consent. A different
proved phone or invalid/expired selection cannot restore that proposal.

Firestore persists the pending-verification marker and price snapshot. The
marker is workflow state, not proof; request-scoped verified ownership is still
required at creation. Profile merges preserve only this chat's matching pending
proposal. Existing expiry, attempts, send budgets, transcript redaction and
send-uncertainty rules remain in place. This backend policy does not enable
Altegio's disabled native web checkbox or confer carrier/aggregator approval.

The original focused verification checks and the full local Go test/vet suite
passed on 15 October. New regression checks cover Instagram/Facebook SMS to
booking completion, Telegram profile-merge continuation, matching and alternate
WhatsApp phones, older drafts, wrong-phone proof, unavailable SMS and code
redaction. The Firestore draft round-trip test also checks the persisted fields
when the emulator is available. Production release validation must pass the
race/emulator and vulnerability gates and confirm the exact live version before
claiming deployment. Local evidence is saved under
`docs/evidence/sms-followup-2026-10-15/` (ignored PNGs).

## References

- [Altegio SMS aggregator onboarding](https://alteg.io/en/support/knowledge-base/6746468657949/)
- [Messaggio native Altegio integration](https://messaggio.com/guides/integration-with-altegio/)
- [Messaggio Armenia OTP and indicative SMS pricing](https://messaggio.com/sms-verification-otp-service-provider/armenia/)
- [Ucom official support contact](https://www.ucom.am/en/help)
- [Messaggio official sales and support addresses](https://messaggio.com/contacts/)
- [SMS.to official Altegio activation guide](https://support.sms.to/support/solutions/articles/43000693902-altegio)
- [SMSGate sending and explicit SIM selection](https://docs.sms-gate.app/features/sending-messages/)
- [Backend notification configuration](native-notifications.md)
