# Raw API request

Use `api request` for a documented endpoint that does not yet have a typed
command. Paths must stay relative to Samsung's allowlisted API families; `gsc`
adds authentication headers itself.

This request is read-only:

```sh
gsc api request \
  --method GET \
  --path '/seller/contentInfo?contentId=000000000000' \
  --profile production \
  --output json
```

For a POST, PUT, PATCH, or DELETE, use `--dry-run` to inspect the request before
any session opens:

```sh
gsc api request \
  --method PATCH \
  --path /iap/v6/applications/com.example.galaxy/items \
  --file examples/iap/item-update.json \
  --profile production \
  --dry-run \
  --output json
```

The raw command does not accept custom authorization headers or absolute URLs.
Prefer a typed command when one exists because it knows the endpoint's
validation and output shape.

