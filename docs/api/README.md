# Galaxy Store API catalog

`gsc` uses a curated API catalog because Samsung does not publish an OpenAPI
document for the Galaxy Store Developer API. The machine-readable source of
truth is embedded from
[`internal/catalog/operations.json`](../../internal/catalog/operations.json).

The catalog was last verified against Samsung's official documentation on
2026-07-30. It covers all public operations in the Access Token, Content
Publish, IAP, and Galaxy Store Statistics (GSS) APIs, plus the public IAP
receipt-verification endpoint.

## Hosts

| Host | Use |
| --- | --- |
| `https://devapi.samsungapps.com` | Authentication, Content Publish, IAP management, and GSS |
| `https://seller.samsungapps.com` | Multipart file upload only |
| `https://iap.samsungapps.com` | Public purchase receipt verification only |

Do not send a Galaxy Store access token to a host that does not require one.
In particular, receipt verification is public and file upload uses the upload
session created through the main Developer API.

## Operation matrix

`Retry` describes transport-level automatic retry policy:

- `safe`: read-only, including Samsung's read-only POST query endpoints.
- `conditional`: only after the client can determine that repeating the request
  cannot create an unwanted duplicate transition.
- `never`: require an explicit caller retry after inspecting the response.

Mutating operations that can publish, delete, refund, revoke, or submit data map
to commands with an explicit `--confirm` gate.

### Access tokens

| ID | Method and path | Scope / auth | Retry | Mutation | Capability | Proposed command |
| --- | --- | --- | --- | --- | --- | --- |
| `auth.token.create` | `POST /auth/accessToken` | requested / JWT bearer | never | yes | authenticate | `gsc auth login` |
| `auth.token.validate` | `GET /auth/checkAccessToken` | token / access token | safe | no | authenticate | `gsc auth status` |
| `auth.token.revoke` | `DELETE /auth/revokeAccessToken` | token / access token | never | yes | destructive | `gsc auth revoke --confirm` |

Samsung access tokens do not expire automatically. They remain active until
revoked or cancelled, so a successful create call must be stored as a durable
credential rather than refreshed on every invocation.

### Content, binaries, submission, and upload

| ID | Method and path | Retry | Mutation | Capability | Proposed command |
| --- | --- | --- | --- | --- | --- |
| `content.apps.list` | `GET /seller/contentList` | safe | no | read | `gsc apps list` |
| `content.apps.view` | `GET /seller/contentInfo` | safe | no | read | `gsc apps view --content-id <content-id>` |
| `content.apps.update` | `POST /seller/contentUpdate` | never | yes | write | `gsc apps update --content-id <content-id> --file <metadata.json> --confirm` |
| `content.binary.add` | `POST /seller/v2/content/binary` | never | yes | upload | `gsc binaries add --content-id <content-id> --file-key <uploaded-file-key> --gms Y\|N --confirm` |
| `content.binary.update` | `PUT /seller/v2/content/binary` | never | yes | upload | `gsc binaries update --content-id <content-id> --binary-seq <sequence> --gms Y\|N --confirm` |
| `content.binary.delete` | `DELETE /seller/v2/content/binary` | never | yes | destructive | `gsc binaries delete --content-id <content-id> --binary-seq <sequence> --confirm` |
| `content.apps.submit` | `POST /seller/contentSubmit` | never | yes | submit | `gsc apps submit --content-id <content-id> --confirm` |
| `content.apps.status.update` | `POST /seller/contentStatusUpdate` | never | yes | publish | `gsc apps status update --content-id <content-id> --status <status> --confirm` |
| `upload.session.create` | `POST /seller/createUploadSessionId` | conditional | yes | upload | `gsc uploads sessions create --confirm` |
| `upload.file` | `POST /galaxyapi/fileUpload` | conditional | yes | upload | `gsc uploads file --session-id <session-id> --file <path> --confirm` |

All operations except `upload.file` use the main Developer API host and require
the `publishing` scope, access token, and `service-account-id` header.
`upload.file` uses `https://seller.samsungapps.com`.

The `binaryList` request field was deprecated in March 2025. Starting in July
2026, `contentUpdate` no longer accepts binary edits through that field. `gsc`
therefore models the current `/seller/v2/content/binary` endpoints and must not
fall back to `binaryList`.

For mutable collection fields in `contentUpdate`, omitted, `null`, and `[]` can
mean different things. Command implementations must preserve the caller's JSON
intent and validate destructive empty collections before sending the request.

### Closed beta and staged rollout

| ID | Method and path | Retry | Mutation | Capability | Proposed command |
| --- | --- | --- | --- | --- | --- |
| `beta.view` | `GET /seller/v2/content/betaTest` | safe | no | read | `gsc beta testers list --content-id <content-id> --app-status SALE\|REGISTRATION` |
| `beta.update` | `PUT /seller/v2/content/betaTest` | never | yes | write | `gsc beta testers update --content-id <content-id> --file <beta.json> --confirm` |
| `rollout.rate.view` | `GET /seller/v2/content/stagedRolloutRate` | safe | no | read | `gsc rollouts rate view --content-id <content-id> --app-status SALE\|REGISTRATION` |
| `rollout.rate.update` | `PUT /seller/v2/content/stagedRolloutRate` | never | yes | write | `gsc rollouts rate update --content-id <content-id> --app-status SALE\|REGISTRATION --file <rates.json> --confirm` |
| `rollout.rate.complete` | `PUT /seller/v2/content/stagedRolloutRate` | never | yes | publish | `gsc rollouts rate complete --content-id <content-id> --app-status SALE\|REGISTRATION --confirm` |
| `rollout.binary.list` | `GET /seller/v2/content/stagedRolloutBinary` | safe | no | read | `gsc rollouts binaries list --content-id <content-id> --app-status <status>` |
| `rollout.binary.update` | `PUT /seller/v2/content/stagedRolloutBinary` | never | yes | write | `gsc rollouts binaries update --content-id <content-id> --file <binaries.json> --confirm` |

An app can have `SALE` and `REGISTRATION` variants at the same time. Commands
that address a variant must not infer one from the Seller Portal display status.
Samsung also does not allow an active rollout percentage to decrease.

### Buyer comments

| ID | Method and path | Retry | Mutation | Capability | Proposed command |
| --- | --- | --- | --- | --- | --- |
| `reviews.list` | `GET /seller/v2/content/comment` | safe | no | read | `gsc reviews list --content-id <content-id>` |
| `reviews.reply` | `POST /seller/v2/content/comment/reply` | never | yes | write | `gsc reviews reply --content-id <content-id> --comment-id <comment-id> --country-code <code> --body <text> --confirm` |
| `reviews.reply.delete` | `DELETE /seller/v2/content/comment/reply` | never | yes | destructive | `gsc reviews reply delete --content-id <content-id> --comment-id <comment-id> --confirm` |

### IAP item catalog

| ID | Method and path | Retry | Mutation | Capability | Proposed command |
| --- | --- | --- | --- | --- | --- |
| `iap.items.list` | `GET /iap/v6/applications/{packageName}/items` | safe | no | read | `gsc iap items list --package-name <package-name>` |
| `iap.items.view` | `GET /iap/v6/applications/{packageName}/items/{id}` | safe | no | read | `gsc iap items view --package-name <package-name> --item-id <item-id>` |
| `iap.items.create` | `POST /iap/v6/applications/{packageName}/items` | never | yes | write | `gsc iap items create --package-name <package-name> --file <item.json> --confirm` |
| `iap.items.replace` | `PUT /iap/v6/applications/{packageName}/items` | never | yes | write | `gsc iap items replace --package-name <package-name> --file <item.json> --confirm` |
| `iap.items.update` | `PATCH /iap/v6/applications/{packageName}/items` | never | yes | write | `gsc iap items update --package-name <package-name> --file <patch.json> --confirm` |
| `iap.items.delete` | `DELETE /iap/v6/applications/{packageName}/items/{id}` | never | yes | destructive | `gsc iap items delete --package-name <package-name> --item-id <item-id> --confirm` |

These calls immediately change the live IAP item catalog, even when the app is
for sale. They cannot create or edit subscription catalog products.

### Purchases, subscriptions, orders, and receipts

| ID | Method and path | Retry | Mutation | Capability | Proposed command |
| --- | --- | --- | --- | --- | --- |
| `iap.purchases.consume` | `PATCH /iap/v6/applications/{packageName}/purchases/{purchaseId}` | conditional | yes | financial | `gsc iap purchases consume --package-name <package-name> --purchase-id <purchase-id> --confirm` |
| `iap.purchases.acknowledge` | same endpoint, `action=acknowledge` | conditional | yes | financial | `gsc iap purchases acknowledge --package-name <package-name> --purchase-id <purchase-id> --confirm` |
| `iap.subscriptions.status` | `GET /iap/seller/v6/applications/{packageName}/purchases/subscriptions/{purchaseId}` | safe | no | read | `gsc iap subscriptions status --package-name <package-name> --purchase-id <purchase-id>` |
| `iap.subscriptions.cancel` | same endpoint, `action=cancel` | never | yes | financial | `gsc iap subscriptions cancel --package-name <package-name> --purchase-id <purchase-id> --confirm` |
| `iap.subscriptions.refund` | same endpoint, `action=refund` | never | yes | financial | `gsc iap subscriptions refund --package-name <package-name> --purchase-id <purchase-id> --confirm` |
| `iap.subscriptions.revoke` | same endpoint, `action=revoke` | never | yes | financial | `gsc iap subscriptions revoke --package-name <package-name> --purchase-id <purchase-id> --confirm` |
| `iap.orders.list` | `POST /iap/seller/orders` | safe | no | financial-read | `gsc iap orders list --seller-seq <seller-sequence> [--request-date <YYYYMMDD>]` |
| `iap.receipts.verify` | `GET /iap/v6/receipt?purchaseID=…` | safe | no | financial-read | `gsc iap receipts verify --purchase-id <purchase-id>` |

Orders is a read-only POST query. Receipt verification uses
`https://iap.samsungapps.com` and does not use the Developer API access token.

### GSS metrics

| ID | Method and path | Scope | Retry | Mutation | Proposed command |
| --- | --- | --- | --- | --- | --- |
| `gss.seller.query` | `POST /gss/query/sellerMetric` | gss | safe | no | `gsc stats seller --file <query.json>` |
| `gss.content.query` | `POST /gss/query/contentMetric` | gss | safe | no | `gsc stats content --file <query.json>` |

Both GSS endpoints are read-only POST queries. They require a token that
contains the `gss` scope.

## Seller Portal boundaries

The public API does not cover these workflows. `gsc` should explain the
boundary and link to Seller Portal instead of pretending to support them.

| Workflow | Why it remains portal-only |
| --- | --- |
| New app registration | Samsung explicitly excludes app creation from the Developer API. A 12-digit `contentId` must already exist. |
| Commercial seller approval | This is an account review workflow in Seller Portal. |
| Service account and private-key creation | Seller Portal creates service accounts, grants API access, and issues private keys. |
| Subscription catalog creation/modification | IAP Publish manages item products only. |
| Settlement report download | No public Galaxy Store Developer API endpoint is documented. |
| App category modification | Content Publish returns categories but marks the field as not modifiable. |

## Primary sources

- [Galaxy Store Developer API overview and complete endpoint list](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api.html)
- [Get started and public-API prerequisites](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api/get-started-with-the-gsd-api.html)
- [Access-token creation](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api/create-an-access-token.html)
- [Content Publish reference](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api/content-publish-api/reference.html)
- [IAP Publish API](https://developer.samsung.com/iap/api/iap-publish-api.html)
- [IAP Purchase Acknowledgment API](https://developer.samsung.com/iap/api/iap-purchase-acknowledgment.html)
- [IAP Subscription API](https://developer.samsung.com/iap/api/iap-subscription-api.html)
- [Purchase receipt verification](https://developer.samsung.com/iap/programming-guide/samsung-iap-server-api.html)
