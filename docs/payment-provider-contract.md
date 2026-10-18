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
