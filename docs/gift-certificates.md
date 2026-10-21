# Owned gift certificates

The studio changed its policy on 21 October 2026: native certificate and deposit
types are no longer the public gift catalog. This exception does not change the
source of truth for services, prices, specialists, availability or bookings.

## Live selection

`GET /api/booking/certificate-types` returns studio configuration version 2:
whole AMD, an editable positive amount, suggested 50,000 / 100,000 / 150,000 AMD,
six months from purchase, and a service-certificate selection mode. Amounts use
safe integers; the maximum is a representation boundary, not a new sales policy.
The service picker shares the live booking service query and native IDs/prices;
add-ons and zero-priced services are excluded. A price range remains a range.
No local treatment names, prices, native certificate types or balances are copied.

This release is a preview. It accepts no payment, stores no purchase and issues
no certificate. The UI states this explicitly. A treatment selection does not
promise a fully paid visit or reserve a specialist. Online checkout must obtain
a current server quote before accepting a purchase.

The deposit tab, client balance lookup and booking certificate selector are
removed. `/benefits` returns 410. The signed central bridge also returns 410 for
`certificate-types` and `client-certificates`, after its normal signature gate.
Stale certificate selections and pending drafts are rejected before native reads
or appointment creation. Historical audit data is retained and hidden from the
public booking result. Existing native types, issued certificates, payments and
client balances are not deleted or changed.

## Altegio compatibility and the next payment release

The documented `chain_loyalty_certificate_types_create` operation creates native
types, including their inventory products. Its settings support a face value,
multiple/single use, expiry and service restrictions. `item_type_id=1` allows
services; `item_type_id=3` plus `service_ids` restricts a type to selected services.
A restricted monetary certificate is not automatically a guaranteed one-visit
entitlement: confirm pricing/redemption semantics before selling that promise.

Primary contract:
https://developer.alteg.io/en/b2b-v1/openapi/chain-loyalty-programs/chain_loyalty_certificate_types_create

Before checkout is enabled, implement the following as one durable purchase flow:

1. Validate the chosen amount or current native service quote server-side; save
   an immutable order snapshot with currency, native IDs and policy version.
2. Verify the payment provider's signed webhook and amount/currency against that
   order. A browser success redirect is not proof of payment.
3. Resolve the recipient with verified identity. Distinguish payer from recipient;
   do not link by name or unverified email or reveal an existing client card.
4. Use a unique studio product/policy key and native type mapping. Under a lock,
   reuse a compatible type or create one once. Do not create one per customer or
   match types by title alone. Preserve the chosen service restriction and expiry.
5. Issue/sell the certificate through the verified native issuance contract and
   link it to the resolved recipient; persist native type, product, instance and
   client IDs against the order. The exact issuance/sale endpoint, variable-value
   behaviour and required app grants still need verification. Loyalty-card
   issuance is not a substitute for gift-certificate issuance.
6. Make webhook handling idempotent. On an uncertain native write, reconcile the
   existing order/instance before retrying. Never charge or issue twice. Notify
   the client only after issuance is confirmed; provide a recoverable pending
   state otherwise. Refund/reversal is a separate explicit state transition.

No payment provider, financial write permission or native storefront has been
activated by this release. Enabling checkout also requires end-to-end validation
of paid issuance, identity linkage, partial redemption, expiry and reconciliation.
