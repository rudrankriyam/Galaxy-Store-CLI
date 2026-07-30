# IAP

Read the one-time item catalog without changing it:

```sh
gsc iap items list \
  --package-name com.example.galaxy \
  --profile production \
  --page 1 \
  --size 20 \
  --output table
```

IAP item writes take effect immediately, including while an app is for sale.
Both sample writes stay local because they use `--dry-run`:

```sh
gsc iap items create \
  --package-name com.example.galaxy \
  --file examples/iap/item-create.json \
  --profile production \
  --dry-run \
  --output json

gsc iap items update \
  --package-name com.example.galaxy \
  --file examples/iap/item-update.json \
  --profile production \
  --dry-run \
  --output json
```

Transaction-changing commands also support dry runs:

```sh
gsc iap purchases acknowledge \
  --package-name com.example.galaxy \
  --purchase-id SAMPLE_PURCHASE_ID \
  --profile production \
  --dry-run

gsc iap subscriptions cancel \
  --package-name com.example.galaxy \
  --purchase-id SAMPLE_SUBSCRIPTION_ID \
  --caller admin \
  --profile production \
  --dry-run
```

Orders and receipt verification are reads:

```sh
gsc iap orders list \
  --seller-seq 000000000000 \
  --request-date 20260101 \
  --profile production \
  --output json

gsc iap receipts verify \
  --purchase-id SAMPLE_PURCHASE_ID \
  --output json
```

Receipt verification uses Samsung's public IAP host and does not send a Galaxy
Store Developer API access token.

