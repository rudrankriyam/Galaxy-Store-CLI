# Galaxy Store CLI examples

Every mutating command in this directory uses `--dry-run`. It validates local
arguments and prints a plan without opening a Galaxy Store session. Read-only
commands do contact Samsung when you run them, so they need a configured
profile and access token.

The IDs, package names, email addresses, file keys, and dates are inert sample
values. Replace them with Seller Portal values before running a command against
your account. No private key or access token belongs in this directory.

Run the local fixture check from the repository root:

```sh
go run ./examples/check
```

The examples cover:

- [authentication](auth/README.md)
- [app inspection and status planning](apps/README.md)
- [file upload and v2 binary registration](binaries/README.md)
- [the three-file metadata bundle](metadata/README.md)
- [buyer comments and seller replies](reviews/README.md)
- [closed beta and staged rollout](beta-rollout/README.md)
- [IAP products and transactions](iap/README.md)
- [Galaxy Store Statistics](stats/README.md)
- [the constrained raw API command](api/README.md)

