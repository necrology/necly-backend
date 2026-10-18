# Payment provider adapter contract (C05)

This document pins the provider behaviour exercised by the synthetic fixtures in
`internal/service/payment_providers_test.go` and
`internal/repository/gormrepo/webhook_integration_test.go`. No live charge or
production callback is used by these tests.

## Midtrans

Authoritative sources:

- Snap transaction body: <https://docs.midtrans.com/docs/snap-snap-integration-guide>
- HTTP notification body and signature: <https://docs.midtrans.com/docs/https-notification-webhooks>

Contract used by the adapter:

- `order_id` is the stored order UUID. It is the binding between the callback
  and the payment row because the payment row stores the Snap token, not the
  later `transaction_id`.
- The signature is `SHA512(order_id + status_code + gross_amount + ServerKey)`,
  sent as `signature_key` **inside the JSON body**. The adapter compares it in
  constant time before parsing any state change.
- `gross_amount` is parsed as an integral IDR amount even when Midtrans sends a
  decimal suffix such as `154600.00`. Any fractional, negative or overflowing
  value is rejected.
- `transaction_status` mapping:
  | Midtrans status | Fraud status | Event status |
  |---|---|---|
  | `capture` | `accept` or empty | `paid` |
  | `capture` | `challenge` | `pending` |
  | `capture` | `deny` | `failed` |
  | `settlement` | any | `paid` |
  | `pending` | any | `pending` |
  | `deny`, `failure` | any | `failed` |
  | `cancel` | any | `cancelled` |
  | `expire` | any | `expired` |
  | `refund`, `partial_refund`, `chargeback` | any | `refunded` |
  | anything else | any | rejected with `invalid_payment_event` |
- `settlement_time` is documented in WIB (UTC+7); the adapter converts it to
  UTC. The event key is a SHA-256 over the transaction id, status, fraud status,
  status code and gross amount, so a replay deduplicates while `pending` and
  `settlement` remain distinct events.

## Xendit

Authoritative sources:

- Create Session (`allowed_payment_channels`): <https://docs.xendit.co/apidocs/create-session>
- Session webhook shape: <https://docs.xendit.co/apidocs/webhook-notification-sent-defined-webhook-url-updates-payment-session>
- Payment session status webhooks: <https://docs.xendit.co/docs/payments-api-webhooks>

Contract used by the adapter:

- `reference_id` is the stored order UUID. Session callbacks carry the payment
  session id in `data.id` (or `data.payment_session_id` for `payment.*`
  events); the legacy invoice shape carries `id`/`external_id` at the top level.
- The callback token is compared in constant time.
- `amount` and `currency` are required. A callback without them, or with values
  that differ from the stored order/payment, is rejected without mutation.
- The event key is a SHA-256 over the event name, payment id, order id, status
  and provider `updated` timestamp. A repeated delivery deduplicates; a later
  status for the same session is processed.
- Status mapping: `COMPLETED`/`PAID`/`SETTLED`/`SUCCEEDED` → `paid`,
  `EXPIRED` → `expired`, `CANCELED`/`CANCELLED` → `cancelled`,
  `FAILED`/`FAILURE` → `failed`, anything else → `pending`.

## Transactional invariants

`ApplyPaymentEvent` (PostgreSQL) performs all of the following in one
transaction:

1. Insert the stable event key. `ON CONFLICT DO NOTHING` makes concurrent and
   repeated deliveries a no-op, and the event row is rolled back with any later
   failure.
2. Lock the order and the payment row for `(order_id, provider)`.
3. Reject amount, currency or provider-payment-identity mismatches.
4. Apply only monotonic transitions. A delayed `expired`/`cancelled` notification
   cannot undo `paid`; an expired or cancelled order is not revived.
5. Release inventory only when the previous status was `pending_payment` and
   the new status is `cancelled` or `expired`. Paid, fulfilled and refunded
   stock keeps its reservation.
6. Keep the payment row status in step with the order where a transition is
   legitimate.

A refund is a financial state change, not an automatic stock release. Any
future fulfilment/refund inventory policy needs its own documented decision.

## Optional payment channel (C13)

The canonical request field is `payment_channel`, trimmed and lower-cased. An
empty value keeps the provider's default channel list. The value is part of the
owner+key fingerprint, so the same idempotency key cannot switch channels.

| Canonical | Midtrans `enabled_payments` | Xendit `allowed_payment_channels` |
|---|---|---|
| `qris` | `other_qris` | `QRIS` |
| `shopeepay` | `shopeepay` | `SHOPEEPAY` |
| `bni_va` | `bni_va` | `BNI_VIRTUAL_ACCOUNT` |
| `bri_va` | `bri_va` | `BRI_VIRTUAL_ACCOUNT` |
| `ovo` | rejected | `OVO` |
| `dana` | rejected | `DANA` |
| `linkaja` | rejected | `LINKAJA` |
| `alfamart` | rejected | `ALFAMART` |
| `bsi_va` | rejected | `BSI_VIRTUAL_ACCOUNT` |
| `mandiri_va` | rejected | `MANDIRI_VIRTUAL_ACCOUNT` |

`rejected` means the adapter returns `ErrUnsupportedPaymentChannel` before any
HTTP call; it never silently sends the provider default. Xendit channel
activation is account-specific: an inactive but documented channel is rejected
by Xendit with `INVALID_PAYMENT_CHANNEL`, which the checkout handler exposes as
`400 unsupported_payment_channel`.

The current bank/retail minimum-duration message remains warning-only. The
backend does not reject a short duration while Q1 is unresolved. A server-side
`payment_channel_not_eligible` rule must not be enabled without Project Owner
approval of the exact behaviour and copy change.
