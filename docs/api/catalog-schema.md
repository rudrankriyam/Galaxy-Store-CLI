# Embedded catalog schema

The operation inventory in `internal/catalog/operations.json` is embedded into
the `internal/catalog` Go package. `catalog.Load` parses it with unknown-field
rejection and validates it before returning any data.

## Operation fields

| Field | Meaning |
| --- | --- |
| `id` | Stable, unique operation identifier |
| `name` | Human-readable operation name |
| `method` | HTTP method: GET, POST, PUT, PATCH, or DELETE |
| `host` | Absolute HTTPS origin; explicit because upload and receipt verification use separate hosts |
| `path` | URL path with named `{placeholders}` where applicable |
| `family` | Command/API grouping used for discovery |
| `scope` | Service-account token scope (`publishing`, `gss`, or operation-specific auth scope) |
| `auth` | Required credential/header profile |
| `retry` | `safe`, `conditional`, or `never` |
| `mutation` | Whether the operation changes remote state |
| `capability` | High-level authorization and safety class |
| `proposedCommand` | Canonical `gsc` command surface for the operation |
| `sourceUrl` | Official Samsung page that supports the catalog row |
| `lastVerified` | Verification date in `YYYY-MM-DD` format |
| `notes` | Optional behavior, lifecycle, or safety caveat |

Operations with a shared method and path remain separate when the request body
selects materially different behavior. This applies to purchase consumption
versus acknowledgment and subscription cancel versus refund versus revoke.

## Limitation fields

Limitations describe known Seller Portal-only workflows. Each entry has a
stable `id`, human-readable `name`, precise `reason`, `portalOnly: true`,
official `sourceUrl`, and `lastVerified` date.

## Validation guarantees

The Go validator and tests enforce:

- unique operation and limitation IDs;
- supported HTTP methods and absolute HTTPS URLs;
- valid verification dates;
- a canonical `gsc` command for every supported operation;
- rejection of the non-canonical `get` command leaf in favor of `view`;
- explicit portal-only marking for every limitation;
- presence of every required API family;
- preservation of the separate file-upload and receipt-verification hosts.
