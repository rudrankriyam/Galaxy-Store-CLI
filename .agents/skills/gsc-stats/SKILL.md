---
name: gsc-stats
description: Query Galaxy Store Statistics seller-wide and per-app metrics with the unofficial `gsc` CLI, including installs, revenue, IAP orders, downloads, and ratings. Use when an agent needs to build a GSS request file, retrieve performance data, filter or aggregate metrics, or diagnose a statistics query.
---

# Galaxy Store statistics

## Authenticate for GSS

Use an existing profile that includes the `gss` scope:

```bash
gsc auth status --profile analytics --output json
```

If the profile lacks `gss`, create or refresh it explicitly with both scopes
needed by the workflow:

```bash
gsc auth login \
  --profile analytics \
  --service-account-id "$SAMSUNG_SERVICE_ACCOUNT_ID" \
  --private-key "$SAMSUNG_PRIVATE_KEY_PATH" \
  --scope publishing \
  --scope gss
```

Statistics operations are read-only even though Samsung implements them with
HTTP POST. They intentionally do not use `--dry-run` or `--confirm`.

## Query seller-wide metrics

Create a seller request file:

```json
{
  "metricIds": [
    "total_unique_installs_filter",
    "revenue_total"
  ],
  "periods": [
    {
      "startDate": "2026-07-01",
      "endDate": "2026-07-30"
    }
  ],
  "getDailyMetric": true,
  "getBreakdownsByFilter": false,
  "noContentMetadata": false,
  "filters": {
    "country": [
      "USA",
      "IND"
    ],
    "device": []
  },
  "trendAggregation": "day"
}
```

Run:

```bash
gsc stats seller \
  --file seller-metrics.json \
  --profile analytics \
  --output json
```

Supported seller metric IDs are exactly:

- `total_unique_installs_filter`
- `revenue_total`
- `dn_by_total_dvce`
- `revenue_item`

Do not normalize or correct those identifiers.

## Query one app

Create a content request file with the 12-digit content ID:

```json
{
  "contentId": "000007654321",
  "metricIds": [
    "total_unique_installs_filter",
    "revenue_iap_order_count",
    "daily_rat_score",
    "daily_rat_volumne"
  ],
  "periods": [
    {
      "startDate": "2026-07-01",
      "endDate": "2026-07-30"
    }
  ],
  "noBreakdown": false,
  "filters": {
    "country": [],
    "device": []
  },
  "trendAggregation": "day"
}
```

Run:

```bash
gsc stats content \
  --file content-metrics.json \
  --profile analytics \
  --output json
```

Supported content metric IDs are exactly:

- `total_unique_installs_filter`
- `revenue_total`
- `revenue_iap_order_count`
- `daily_rat_score`
- `daily_rat_volumne`

Keep Samsung's published spelling `daily_rat_volumne`; changing it to
`daily_rat_volume` makes the query invalid.

## Validate request semantics

- Include at least one unique metric ID and at least one period.
- Format dates as `YYYY-MM-DD` and keep each start date on or before its end
  date.
- Set `trendAggregation` to exactly `day`, `week`, or `month`.
- Keep country and device filter values non-empty, unpadded, and unique.
- Keep the request at most 1 MiB and provide exactly one JSON object.
- Use `--output json` for scripts and CI; use table or Markdown only for human
  inspection.

Galaxy Store Statistics data is processed on Samsung's daily GMT schedule.
Treat an empty or not-yet-updated period as data latency until the same query
and service scope have been verified.
