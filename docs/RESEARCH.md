# Galaxy Store CLI landscape

Last verified: 2026-07-30

This document records why `gsc` exists and the evidence behind its positioning.
It is intentionally precise: tools can publish to Galaxy Store today, but
Samsung does not provide an official CLI and no existing dedicated CLI covers
the full Galaxy Store Developer API.

## Conclusion

`gsc` is positioned as the first dedicated, comprehensive Galaxy Store CLI
modeled after mature app-store CLIs.

It is not the first tool capable of uploading a Galaxy Store binary. The
strongest current competitor is `apkgo`, an active multi-store publisher with a
current Galaxy adapter. The gap is a first-class Galaxy developer tool that
covers authentication, apps, metadata, binary lifecycle, submission, beta,
rollouts, buyer comments, IAP, orders, subscription operations, statistics,
raw API access, deterministic automation output, and agent skills.

## Official Samsung surface

Samsung's official automation surface is the server-to-server
[Galaxy Store Developer API](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api.html)
plus Seller Portal. No Samsung-published CLI was found after checking Samsung
documentation, GitHub, Gradle Plugin Portal, RubyGems, PyPI, and npm.

The official API includes:

- Content Publish API
- IAP Publish, Orders, Purchase Acknowledgment, and Subscription APIs
- Galaxy Store Statistics Metric API

The public API cannot register a new app. A seller must first create the app in
Seller Portal and obtain its 12-digit `contentId`. Commercial seller enrollment
and service-account creation are also portal-only. See Samsung's
[getting-started guide](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api/get-started-with-the-gsd-api.html).

## Material competitors

| Tool | Adoption/activity | Galaxy scope | Main gap versus `gsc` |
| --- | --- | --- | --- |
| [`apkgo`](https://github.com/KevinGong2013/apkgo) | 141 stars; active; v3.6.3 on 2026-07-28 | Multi-store upload, submit, scheduled publication, status audit, doctor | Galaxy is one adapter; no broad Galaxy resource, metadata-sync, beta, rollout, comments, IAP, orders, subscription, GSS, or token-lifecycle command surface |
| [`mozapkpublisher`](https://github.com/mozilla-releng/mozapkpublisher) | 10 stars; active; 11.0.2 on 2026-07-16 | Project-oriented Samsung upload, submit, rollout, cleanup | Internal Python publisher, not a complete Galaxy developer CLI |
| [`shipup`](https://github.com/HenryHaoson/shipup) | 1 star; npm 0.3.0 on 2026-07-13 | Multi-store upload, listing fields, media, submit, status | Very new; Galaxy is one adapter; lacks most Galaxy APIs |
| [`galaxy-store-publisher-gradle`](https://github.com/sergei-lapin/galaxy-store-publisher-gradle) | 9 stars; pushed 2026-02-26 | Gradle variant upload | Narrow and currently sends deprecated `binaryList` through `contentUpdate` |
| [`fastlane-plugin-galaxy_store_developer`](https://github.com/RimiX2/fastlane-plugin-galaxy_store_developer) | 2 stars; one commit; last pushed 2023-05-24 | List, info, upload, submit | Narrow, hard-coded behavior, and deprecated binary update path |
| [`galaxy-store-submit`](https://github.com/TheOriginalAyaka/galaxy-store-submit) | 1 star; one commit; no release | GitHub Action for upload and optional submit | Action only; no general CLI or wider API coverage |
| [`app-store-publisher-mcp`](https://github.com/qalvinahmad/app-store-publisher-mcp) | 0 stars; one commit; created 2026-07-04 | MCP token, app detail, raw request, receipt verification | MCP server, not a CLI; no lifecycle orchestration |
| [`samsung-galaxy-store`](https://github.com/minormending/samsung-galaxy-store) | 3 stars; last source push 2022-08-08 | Public store categories, app details, reviews | Consumer-side scraper; no Seller API auth, upload, submit, or publishing |

Other narrow tools reviewed:

- [`Litres/samsung-publisher-gradle-plugin`](https://github.com/Litres/samsung-publisher-gradle-plugin)
- [`BioforestChain/android-auto-distribute`](https://github.com/BioforestChain/android-auto-distribute)

The existing PyPI `samsung-galaxy-store` package installs a
`galaxy-store-cli` executable. That name belongs to a stale read-only public
store scraper, not a publishing CLI. This project therefore uses the `gsc`
binary.

## July 2026 binary compatibility

Samsung deprecated `contentUpdate.binaryList` in March 2025. Starting in July
2026, a `contentUpdate` request containing `binaryList` fails. Current tools
must use:

- `POST /seller/v2/content/binary`
- `PUT /seller/v2/content/binary`
- `DELETE /seller/v2/content/binary`

The transition is documented in Samsung's
[Content Publish API reference](https://developer.samsung.com/galaxy-store/galaxy-store-developer-api/content-publish-api/reference.html).

Current as of this audit:

- `apkgo`, `mozapkpublisher`, `shipup`, and `galaxy-store-submit` use the v2
  binary API.
- `galaxy-store-publisher-gradle`,
  `fastlane-plugin-galaxy_store_developer`,
  `samsung-publisher-gradle-plugin`, and `android-auto-distribute` still rely
  on the rejected `binaryList` flow.

`gsc` must never send `binaryList` in a metadata update. The operation catalog
and tests treat the v2 binary endpoints as the only supported binary mutation
surface.

## Product standard

The bar is not merely "can upload an AAB." A complete Galaxy Store CLI needs:

- secure service-account token lifecycle and named profiles
- lossless JSON plus stable interactive tables
- deterministic exit codes and stdout/stderr separation
- read-only app discovery and state resolution
- safe metadata pull, diff, validation, and apply
- streaming file upload and v2 binary CRUD
- submission, publication, status wait, and recovery workflows
- closed beta and staged rollout management
- buyer comments and replies
- IAP items, purchase acknowledgment, orders, and subscription operations
- seller and content GSS metrics
- guarded raw API access
- dry-run plans, explicit confirmation, and mutation readback
- GitHub Actions, cross-platform snapshots, Homebrew, and WinGet readiness
- agent skills that encode safe, repeatable workflows

That complete surface—not an unsupported claim of being the first uploader—is
the differentiation.
