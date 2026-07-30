# Apps and distribution status

Samsung may return both a published `SALE` record and an in-progress
`REGISTRATION` record for the same content ID. `apps view` keeps both records:

```sh
gsc apps view \
  --content-id 000000000000 \
  --profile production \
  --output json
```

Changing distribution status is separate from submitting a registration for
review. This command only prints the proposed transition:

```sh
gsc apps status update \
  --content-id 000000000000 \
  --status SUSPENDED \
  --profile production \
  --dry-run \
  --output table
```

Use `FOR_SALE`, `SUSPENDED`, or `TERMINATED` for `--status`. Termination has
extra lifecycle constraints; inspect the plan before deciding whether to run
the confirmed command.

