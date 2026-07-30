# Galaxy Store Statistics

Statistics calls are read-only POST queries. They need a profile whose token
contains the `gss` scope:

```sh
gsc stats seller \
  --file examples/stats/seller.json \
  --profile analytics \
  --output table

gsc stats content \
  --file examples/stats/content.json \
  --profile analytics \
  --output json
```

GSS date ranges use `YYYY-MM-DD` in GMT. The `contentId` belongs inside the
content query file, not on the command line.

