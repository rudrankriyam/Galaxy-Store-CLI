---
name: gsc-iap-management
description: Inspect and manage Galaxy Store in-app purchases with the unofficial `gsc` CLI, including one-time item catalogs, purchase fulfillment, subscription status and interventions, order history, and receipt verification. Use when an agent needs to operate Samsung IAP safely or choose the correct IAP action.
---

# Galaxy Store IAP management

## Select the correct operation

- Use `iap items` for one-time item catalog records.
- Use `iap purchases consume` after fulfilling a consumable purchase.
- Use `iap purchases acknowledge` after granting subscription entitlement.
- Use `iap subscriptions status` before any subscription intervention.
- Use `iap subscriptions cancel` to stop renewal while preserving entitlement
  through the paid period.
- Use `iap subscriptions refund` to refund the latest payment while leaving the
  subscription active.
- Use `iap subscriptions revoke` to remove entitlement immediately and refund
  the latest payment.
- Use `iap orders list` to inspect payments and refunds.
- Use `iap receipts verify` to verify a purchase against Samsung's public
  receipt endpoint.

Do not substitute cancel, refund, and revoke for one another. Each has a
different customer and financial effect.

Use a profile with the `publishing` scope for authenticated IAP commands:

```bash
gsc auth status --profile production --output json
```

## Manage one-time items

List and inspect catalog items:

```bash
gsc iap items list \
  --package-name com.example.app \
  --page 1 \
  --size 20 \
  --profile production \
  --output json

gsc iap items view \
  --package-name com.example.app \
  --item-id coins_100 \
  --profile production \
  --output json
```

`create` and `replace` require a complete JSON object. `replace` overwrites all
mutable item information, so first read the current item and preserve every
field that must remain:

```json
{
  "id": "coins_100",
  "title": "100 Coins",
  "description": "Adds 100 coins",
  "type": "ITEM",
  "status": "PUBLISHED",
  "itemPaymentMethod": {
    "phoneBillStatus": true
  },
  "usdPrice": 0.99,
  "prices": [
    {
      "countryId": "USA",
      "currency": "USD",
      "localPrice": "0.99"
    }
  ]
}
```

Use `update` only for a title and/or local territory prices. Its JSON shape is:

```json
{
  "id": "coins_100",
  "title": "100 Galaxy Coins",
  "prices": [
    {
      "countryId": "USA",
      "localPrice": "1.09"
    }
  ]
}
```

Run every catalog mutation first with `--dry-run`, then repeat the exact command
with `--confirm`:

```bash
gsc iap items update \
  --package-name com.example.app \
  --file item-update.json \
  --profile production \
  --output json \
  --dry-run

gsc iap items update \
  --package-name com.example.app \
  --file item-update.json \
  --profile production \
  --output json \
  --confirm
```

The same pattern applies to `create`, `replace`, and:

```bash
gsc iap items delete \
  --package-name com.example.app \
  --item-id coins_100 \
  --profile production \
  --output json \
  --dry-run
```

Catalog mutations apply immediately. Subscription product registration remains
Seller Portal-only; do not claim that `gsc iap items` manages subscription
products.

## Fulfill purchases

Consume a consumable only after successful server-side fulfillment:

```bash
gsc iap purchases consume \
  --package-name com.example.app \
  --purchase-id PURCHASE_ID \
  --profile production \
  --output json \
  --dry-run
```

Acknowledge subscription entitlement only after it has been granted:

```bash
gsc iap purchases acknowledge \
  --package-name com.example.app \
  --purchase-id PURCHASE_ID \
  --profile production \
  --output json \
  --dry-run
```

Use repeated `--purchased-id ID` flags only when Samsung's batch body is
required. After reviewing the plan, replace `--dry-run` with `--confirm`.
Avoid blind retries after ambiguous timeouts; verify current purchase state.

## Operate subscriptions

Always inspect status first:

```bash
gsc iap subscriptions status \
  --package-name com.example.app \
  --purchase-id SUBSCRIPTION_PURCHASE_ID \
  --profile production \
  --output json
```

Then preview the specifically authorized action:

```bash
gsc iap subscriptions cancel \
  --package-name com.example.app \
  --purchase-id SUBSCRIPTION_PURCHASE_ID \
  --caller admin \
  --profile production \
  --output json \
  --dry-run

gsc iap subscriptions refund \
  --package-name com.example.app \
  --purchase-id SUBSCRIPTION_PURCHASE_ID \
  --profile production \
  --output json \
  --dry-run

gsc iap subscriptions revoke \
  --package-name com.example.app \
  --purchase-id SUBSCRIPTION_PURCHASE_ID \
  --profile production \
  --output json \
  --dry-run
```

For cancel, `--caller` is `admin` by default and may be set to `user`. Repeat
only the chosen command with `--confirm`.

## Inspect orders

Use the 12-digit Seller Portal deeplink number:

```bash
gsc iap orders list \
  --seller-seq 000123456789 \
  --package-name com.example.app \
  --request-date 20260730 \
  --profile production \
  --output json
```

If omitted, `--request-date` defaults to yesterday. Samsung returns up to 100
records; pass the returned token as `--continuation-token` for the next page.
The service uses HTTP POST for this read-only operation, so the command
intentionally has no confirmation flags.

## Verify receipts

Receipt verification is intentionally unauthenticated and has no `--profile`:

```bash
gsc iap receipts verify --purchase-id PURCHASE_ID --output json
```

Inspect the emitted receipt even when the process exits nonzero. A `fail` or
`cancel` receipt is data to reconcile, not a transport failure, and `gsc`
prints the safe response before returning failure.
