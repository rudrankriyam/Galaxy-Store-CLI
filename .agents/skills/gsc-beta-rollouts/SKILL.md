---
name: gsc-beta-rollouts
description: Manage Galaxy Store closed-beta testers and staged rollout rates or binaries with the unofficial `gsc` CLI. Use when an agent needs to inspect or update beta access, add or remove testers, start or advance a percentage rollout, change rollout binaries, or complete a rollout globally.
---

# Galaxy Store beta and rollouts

## Choose the app variant explicitly

Samsung can expose `SALE` and `REGISTRATION` records for one content ID. Pass
`--app-status SALE` or `--app-status REGISTRATION` whenever the command accepts
it. Do not infer the variant.

Use a profile with the `publishing` scope and use JSON in automation:

```bash
gsc auth status --profile production --output json
gsc apps view --content-id 000007654321 --profile production --output json
```

## Manage closed-beta testers

List one page, up to 1,000 testers:

```bash
gsc beta testers list \
  --content-id 000007654321 \
  --app-status REGISTRATION \
  --offset 0 \
  --limit 1000 \
  --profile production \
  --output json
```

Prepare an update file with only the fields that should change:

```json
{
  "betaTestersToBeAdded": [
    "tester-one@samsung.com",
    "tester-two@samsung.com"
  ],
  "betaTestersToBeDeleted": [
    "former-tester@samsung.com"
  ],
  "feedbackChannel": "beta-feedback@example.com"
}
```

Apply it in two deliberate calls:

```bash
gsc beta testers update \
  --content-id 000007654321 \
  --file testers.json \
  --profile production \
  --output json \
  --dry-run

gsc beta testers update \
  --content-id 000007654321 \
  --file testers.json \
  --profile production \
  --output json \
  --confirm
```

Keep each request to at most 1,000 additions and 1,000 deletions. Samsung allows
up to 20,000 testers in a beta. If an account appears in both arrays, deletion
wins.

Inspect `additionFailedTesters` and `deletionFailedTesters` in the JSON response.
Samsung can accept the outer request while rejecting individual account IDs;
do not treat an empty process error alone as complete success.

## View and advance a staged rollout

Read the current rate before planning a change:

```bash
gsc rollouts rate view \
  --content-id 000007654321 \
  --app-status SALE \
  --profile production \
  --output json
```

Create a strict rate file. Rates are integer percentages from 1 through 100:

```json
{
  "rolloutRate": 25,
  "countries": [
    {
      "countryCode": "USA",
      "rolloutRate": 30
    },
    {
      "countryCode": "IND",
      "rolloutRate": 35
    }
  ]
}
```

Country codes must be unique, uppercase, three-letter codes. Preview and
confirm:

```bash
gsc rollouts rate update \
  --content-id 000007654321 \
  --app-status SALE \
  --file rates.json \
  --profile production \
  --output json \
  --dry-run

gsc rollouts rate update \
  --content-id 000007654321 \
  --app-status SALE \
  --file rates.json \
  --profile production \
  --output json \
  --confirm
```

The dry run validates the local file and prints the planned request; it does
not read Samsung state. During confirmed execution, `gsc` reads the current
rollout and rejects any default or existing country rate that does not strictly
increase. For a `SALE` rollout, every submitted country rate must also be
greater than the submitted default rate. Advance or omit country overrides in
later updates until the default catches up.

## Select rollout binaries

List binaries for the exact variant:

```bash
gsc rollouts binaries list \
  --content-id 000007654321 \
  --app-status REGISTRATION \
  --profile production \
  --output json
```

Use one `ADD` or `REMOVE` operation per file:

```json
{
  "function": "ADD",
  "binarySeq": "123456"
}
```

```bash
gsc rollouts binaries update \
  --content-id 000007654321 \
  --file binary.json \
  --profile production \
  --output json \
  --dry-run

gsc rollouts binaries update \
  --content-id 000007654321 \
  --file binary.json \
  --profile production \
  --output json \
  --confirm
```

Do not invent binary actions beyond `ADD` and `REMOVE`.

## Complete globally

Only complete after verifying the current default and country rates:

```bash
gsc rollouts rate view \
  --content-id 000007654321 \
  --app-status SALE \
  --profile production \
  --output json

gsc rollouts rate complete \
  --content-id 000007654321 \
  --app-status SALE \
  --profile production \
  --output json \
  --dry-run

gsc rollouts rate complete \
  --content-id 000007654321 \
  --app-status SALE \
  --profile production \
  --output json \
  --confirm
```

Treat `complete` as distribution to 100% of users. Samsung calls the underlying
operation `DISABLE_ROLLOUT`, but it is not a pause, stop, or rollback. Obtain
explicit authorization for global completion.
