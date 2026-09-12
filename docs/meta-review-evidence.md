# Meta review evidence checklist

Updated 12 September 2026. The WhatsApp test transport and signed sample webhook
are live; real-conversation evidence is still required. See
[account setup status](meta-onboarding-status.md) for completed steps and
[channel setup](meta-setup.md) for the implemented transports.

## Public app identity

- App: Motion Concept AI Bot chat (`3531775307007016`).
- Business: Motion Concept Studio / E-motion Concept Massage Studio, Armenia.
- Website: <https://www.motionconcept.rest/>.
- Privacy: <https://www.motionconcept.rest/privacy>.
- Data deletion: <https://www.motionconcept.rest/data-deletion>.
- Assistant terms: <https://www.motionconcept.rest/terms>.
- Icon and public URLs are saved; Meta App settings shows 100%.

## WhatsApp evidence still needed

1. Completed: created Meta's free test number `+1 555-195-8338`, WABA
   `1720864419030467`, phone ID `1350823718109876`. Test token available in Meta;
   keep it out of source control and screen recordings.
2. Completed: the studio number ending `8067` is listed as a verified recipient.
3. Meta marks **Send a message from your test number** as **Completed** on the
   resumed 11 September session. Message ID, actual template and delivery receipt
   have not been observed; the dashboard status does not establish an end-to-end
   bot reply. Do not record token values or unrelated customer information.
4. Completed: read-only `GET /v26.0/1720864419030467/phone_numbers` returned
   the matching test phone ID and `CLOUD_API` on 11 September. Both permissions
   showed 0 of 1 required calls before this call. On 12 September Meta showed
   **Completed** for the API-call check in both WhatsApp permission cards.
5. Completed: deployed the WhatsApp transport, verified the callback, subscribed
   `messages` and `smb_message_echoes`, and received Meta's signed `messages`
   sample with HTTP 200. Still required: verify a real incoming message and text
   reply using the designated test recipient. A dashboard sample or template
   message alone does not demonstrate the booking assistant.
6. Record an actual end-to-end screencast of the integration. Hide credentials,
   unrelated conversations and customer data. Demonstrate the implemented flow
   rather than using mock screens or Telegram footage as WhatsApp evidence.

## Suggested assistant demonstration

- Customer initiates the WhatsApp conversation; assistant replies with studio
  help and a clear next question.
- Customer asks for face massage services: show only matching category services.
- Customer selects a service: show eligible specialists and real available times.
- Customer sends a short voice question: show a relevant **text** answer.
- Customer asks for a person: show staff handoff and the resulting customer reply.
- If booking creation needs to be demonstrated, use an approved test booking
  environment or obtain explicit authorization for the actual appointment and
  its cleanup. Do not silently create a real studio appointment for a recording.

The bot changes are merged to `main` and deployed as runtime version `ecd7520`.
Coexistence contact/history sync and manual-phone message mirroring are
implemented, and `smb_message_echoes` is subscribed. Claiming and configuring a
Meta test number does not resolve the existing number's advanced-permission
approval gate.

## Draft accuracy before submission

- Confirm processor legal names, service categories and all relevant processing
  countries, including remote access, against the actual provider arrangements.
  A vendor headquarters address or one cloud region is not sufficient evidence.
- Obtain the user's answers about national-security disclosures in the preceding
  12 months and their existing public-authority request processes.
- Completed: aligned the requested permissions with the implemented transports.
  Instagram now requests the Instagram Login variants. Unused profile,
  demographic, advertising, utility/marketing messaging and Human Agent requests
  have been removed. Messenger keeps its send permission and the Page discovery /
  webhook subscription dependencies displayed by Meta. WhatsApp retains messaging,
  management and portfolio onboarding permissions.
- Replace draft development-status reviewer instructions with reproducible
  instructions for the working Meta integration when it is actually available.
- Complete allowed-usage attestations only after their claims are verified and
  any required user confirmation is obtained. Keep the review unsubmitted until
  the actual demonstration and required answers are ready.
