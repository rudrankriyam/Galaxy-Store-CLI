# Closed beta and staged rollout

Always select `SALE` or `REGISTRATION` for reads that address an app variant:

```sh
gsc beta testers list \
  --content-id 000000000000 \
  --app-status REGISTRATION \
  --profile production \
  --output table

gsc rollouts rate view \
  --content-id 000000000000 \
  --app-status REGISTRATION \
  --profile production \
  --output json

gsc rollouts binaries list \
  --content-id 000000000000 \
  --app-status REGISTRATION \
  --profile production \
  --output table
```

The fixture files can be validated with dry runs:

```sh
gsc beta testers update \
  --content-id 000000000000 \
  --file examples/beta-rollout/testers.json \
  --dry-run

gsc rollouts rate update \
  --content-id 000000000000 \
  --app-status REGISTRATION \
  --file examples/beta-rollout/rates.json \
  --dry-run

gsc rollouts binaries update \
  --content-id 000000000000 \
  --file examples/beta-rollout/binary.json \
  --dry-run
```

Samsung allows rollout percentages to increase, never decrease. Completing a
rollout distributes the release to everyone; it does not stop the rollout.

