# Galaxy Store CLI

The unofficial CLI for Galaxy Store developers.

`gsc` is an unofficial, automation-first CLI for the
[Galaxy Store Developer API](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api.html).
It is designed for developers and CI systems that need a safe, scriptable
alternative to repetitive Seller Portal work.

> [!IMPORTANT]
> This project is not affiliated with, endorsed by, or sponsored by Samsung.
> Galaxy Store and Samsung are trademarks of Samsung Electronics Co., Ltd.

## Status

The command foundation is under active development. No release has been
published. Build from source while the command surface is being completed:

```bash
go build -o ./bin/gsc .
./bin/gsc --help
```

## Design principles

- JSON on pipes and stable tables in interactive terminals
- explicit confirmation and dry-run plans before mutations
- credentials kept out of command output, logs, and repository files
- current Galaxy Store API behavior, including the v2 binary endpoints
- deterministic exit codes and stdout/stderr separation for CI
- discoverable commands and companion skills for coding agents

## Seller Portal prerequisites

Samsung requires a Seller Portal account with commercial seller status, an app
registered in Seller Portal, and a service account with the appropriate
`publishing` and/or `gss` scopes. The API cannot register a brand-new app.

See [Samsung's getting-started guide](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api/get-started-with-the-gsd-api.html).

## License

MIT
