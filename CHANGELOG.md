# Changelog

## [1.10.1](https://github.com/nonchan7720/manifold/compare/v1.10.0...v1.10.1) (2026-08-26)


### Bug Fixes

* **deps:** update go patch dependencies ([#134](https://github.com/nonchan7720/manifold/issues/134)) ([8a1b9a9](https://github.com/nonchan7720/manifold/commit/8a1b9a94a18a8692f81175d0b4ea7966c809fa18))
* **deps:** update module github.com/stretchr/testify to v1.12.1 ([#129](https://github.com/nonchan7720/manifold/issues/129)) ([1410ccf](https://github.com/nonchan7720/manifold/commit/1410ccfd5499683a3bebe73669067d4c6cd5b682))


### Miscellaneous

* **deps:** update docker/setup-buildx-action action to v4.3.0 ([#130](https://github.com/nonchan7720/manifold/issues/130)) ([38263bc](https://github.com/nonchan7720/manifold/commit/38263bc41d46c20b7536a9fafdd94780e256b02d))

## [1.10.0](https://github.com/nonchan7720/manifold/compare/v1.9.0...v1.10.0) (2026-08-26)


### Features

* periodically refresh OpenAPI specs and update MCP tools ([#127](https://github.com/nonchan7720/manifold/issues/127)) ([06cd8de](https://github.com/nonchan7720/manifold/commit/06cd8decc21582abe64d86f45e76232b486dff27))


### Miscellaneous

* remove dead code identified by deadcode analysis ([#125](https://github.com/nonchan7720/manifold/issues/125)) ([970780e](https://github.com/nonchan7720/manifold/commit/970780e442bb290e9edb8d39c872ddb91ed58e04))

## [1.9.0](https://github.com/nonchan7720/manifold/compare/v1.8.0...v1.9.0) (2026-08-26)


### Features

* support for binary to application/json ([#121](https://github.com/nonchan7720/manifold/issues/121)) ([af39da7](https://github.com/nonchan7720/manifold/commit/af39da78efeb50b2632552977ee014324c57a95b))


### Bug Fixes

* **deps:** update module github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager to v0.3.14 ([#124](https://github.com/nonchan7720/manifold/issues/124)) ([ce209f9](https://github.com/nonchan7720/manifold/commit/ce209f9584f7bca7447248c6f622f7b9cf4b8ed4))
* **deps:** update module github.com/getkin/kin-openapi to v0.147.0 ([#122](https://github.com/nonchan7720/manifold/issues/122)) ([a85a915](https://github.com/nonchan7720/manifold/commit/a85a915f388d2a430d4a443ecb6d594843d91881))

## [1.8.0](https://github.com/nonchan7720/manifold/compare/v1.7.0...v1.8.0) (2026-08-25)


### Features

* WebMCP reverse gateway Phase 2a — edge token の複数バインディング ([#105](https://github.com/nonchan7720/manifold/issues/105)) ([3c69934](https://github.com/nonchan7720/manifold/commit/3c699349e0c29b079871eace1fc841eef103d701))
* WebMCP reverse gateway Phase 2a — identities config + identity 解決器 ([#104](https://github.com/nonchan7720/manifold/issues/104)) ([a406c23](https://github.com/nonchan7720/manifold/commit/a406c23ea9fdb1e6e3da5b686db8319367c8c271))
* WebMCP reverse gateway Phase 2a — remote pairing の有効化とルーティング ([#111](https://github.com/nonchan7720/manifold/issues/111)) ([176767d](https://github.com/nonchan7720/manifold/commit/176767d627db0f2ad0ef32810ec537baf6434e54))


### Bug Fixes

* **deps:** update dependency uuid to v14 ([#103](https://github.com/nonchan7720/manifold/issues/103)) ([03ca9d7](https://github.com/nonchan7720/manifold/commit/03ca9d7d1a1a1d1d00c1447e9cf3e5b9725c1f59))
* **deps:** update go patch dependencies ([#110](https://github.com/nonchan7720/manifold/issues/110)) ([02121c2](https://github.com/nonchan7720/manifold/commit/02121c22457ae2cf98a554864699d4bfe7415d13))
* **deps:** update module github.com/micahparks/jwkset to v0.11.3 ([#108](https://github.com/nonchan7720/manifold/issues/108)) ([ce911f5](https://github.com/nonchan7720/manifold/commit/ce911f5a5b81fdf2a58e81d99e9a83a347010bc9))
* **deps:** update module github.com/stretchr/testify to v1.12.0 ([#90](https://github.com/nonchan7720/manifold/issues/90)) ([4930dda](https://github.com/nonchan7720/manifold/commit/4930dda15c2a5ada01ec03831cf64d0b0e70dfc0))
* multipart form data parse ([#118](https://github.com/nonchan7720/manifold/issues/118)) ([#120](https://github.com/nonchan7720/manifold/issues/120)) ([0167c9b](https://github.com/nonchan7720/manifold/commit/0167c9bc1d0ecf83b89bd1a7de824f302b1d2b0c))
* wrap array to items object ([#116](https://github.com/nonchan7720/manifold/issues/116)) ([284ef44](https://github.com/nonchan7720/manifold/commit/284ef44a236e765600db4a681f71b2eba8dd2371))


### Miscellaneous

* **deps:** pin docker/dockerfile docker tag to ecfaec9 ([#99](https://github.com/nonchan7720/manifold/issues/99)) ([7aa33ba](https://github.com/nonchan7720/manifold/commit/7aa33ba9e009e984576f9819c18cf0ccc954ebb4))


### Documentation

* refactor README ([#112](https://github.com/nonchan7720/manifold/issues/112)) ([094aac4](https://github.com/nonchan7720/manifold/commit/094aac4a8b4326f714ef1a29d57691ed6cb61c7a))

## [1.7.0](https://github.com/nonchan7720/manifold/compare/v1.6.3...v1.7.0) (2026-08-21)


### Features

* WebMCP reverse connection gateway (pairing + static mode) ([#100](https://github.com/nonchan7720/manifold/issues/100)) ([807bc5b](https://github.com/nonchan7720/manifold/commit/807bc5b9785e061096534a6db9139373f1d65666))


### Bug Fixes

* address code review findings in CIMD resource validation ([#91](https://github.com/nonchan7720/manifold/issues/91)) ([ddd0afc](https://github.com/nonchan7720/manifold/commit/ddd0afc412f6b90793b361db8aec02edbcdf9507))
* **deps:** update go patch dependencies ([#98](https://github.com/nonchan7720/manifold/issues/98)) ([f553073](https://github.com/nonchan7720/manifold/commit/f5530737a83bcf9ab9fc495af94c906eb1ca1552))


### Miscellaneous

* **deps:** pin dependencies ([#96](https://github.com/nonchan7720/manifold/issues/96)) ([d2e4092](https://github.com/nonchan7720/manifold/commit/d2e4092dca1dffaf2422102fdead8258fef88b80))
* **deps:** pin golang docker tag to 116d58c ([#97](https://github.com/nonchan7720/manifold/issues/97)) ([3de9b98](https://github.com/nonchan7720/manifold/commit/3de9b983e503e43b11832598c180258f69a18619))
* **deps:** update grafana/otel-lgtm docker tag to v0.30.1 ([#94](https://github.com/nonchan7720/manifold/issues/94)) ([b3d6113](https://github.com/nonchan7720/manifold/commit/b3d6113204efaaeb622afe6ec6331b5966f5dbe8))

## [1.6.3](https://github.com/nonchan7720/manifold/compare/v1.6.2...v1.6.3) (2026-08-13)


### Bug Fixes

* **deps:** update all patch updates ([#83](https://github.com/nonchan7720/manifold/issues/83)) ([c6493f3](https://github.com/nonchan7720/manifold/commit/c6493f36527f624ac12a89fdad60bcfa0ac2cda5))
* **deps:** update all patch updates ([#87](https://github.com/nonchan7720/manifold/issues/87)) ([5dab075](https://github.com/nonchan7720/manifold/commit/5dab0752844f982545b844d7f42891f9f02ce40a))
* **deps:** update go minor dependencies ([#81](https://github.com/nonchan7720/manifold/issues/81)) ([8f681cf](https://github.com/nonchan7720/manifold/commit/8f681cfda1635964f1f6f2cd8a4fd72106ce747d))
* **deps:** update go-redis to v9.22.0 ([#82](https://github.com/nonchan7720/manifold/issues/82)) ([d13478e](https://github.com/nonchan7720/manifold/commit/d13478e181e28447dc1f26c0ba07e1369eea6676))
* **deps:** update module github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager to v0.3.11 ([#89](https://github.com/nonchan7720/manifold/issues/89)) ([98a2bdd](https://github.com/nonchan7720/manifold/commit/98a2bdde15fcf061a7abf8537681fccc104db0c0))
* **deps:** update module github.com/go-ozzo/ozzo-validation/v4 to v4.4.1 ([#86](https://github.com/nonchan7720/manifold/issues/86)) ([f0b0e62](https://github.com/nonchan7720/manifold/commit/f0b0e622a3698c4c28d7579b7d51ba17a1c293bf))
* **deps:** update opentelemetry ([#84](https://github.com/nonchan7720/manifold/issues/84)) ([aa6fa39](https://github.com/nonchan7720/manifold/commit/aa6fa39d6f33c04643adce233e23b83dd0cf9f21))


### Code Refactoring

* download content handler ([#88](https://github.com/nonchan7720/manifold/issues/88)) ([a522d8b](https://github.com/nonchan7720/manifold/commit/a522d8bc04d5cb373ea0dbc73ef33bb0d868429a))

## [1.6.2](https://github.com/nonchan7720/manifold/compare/v1.6.1...v1.6.2) (2026-08-08)


### Bug Fixes

* **deps:** update all patch updates ([#76](https://github.com/nonchan7720/manifold/issues/76)) ([59f94af](https://github.com/nonchan7720/manifold/commit/59f94affb4d431244fd20e0322690cf13d4ee596))
* **deps:** update all patch updates ([#78](https://github.com/nonchan7720/manifold/issues/78)) ([3062899](https://github.com/nonchan7720/manifold/commit/3062899e05bc3d1299930dff2bb49b70a72ec287))
* **deps:** update module github.com/compose-spec/compose-go/v2 to v2.14.0 ([#79](https://github.com/nonchan7720/manifold/issues/79)) ([d4906aa](https://github.com/nonchan7720/manifold/commit/d4906aa98020f8e92072bd80497d2b625d71b080))
* **deps:** update module github.com/getkin/kin-openapi to v0.145.0 ([#69](https://github.com/nonchan7720/manifold/issues/69)) ([4214527](https://github.com/nonchan7720/manifold/commit/421452750d5250f989d651ee374bb685c5fe51c3))
* secure http client ([#73](https://github.com/nonchan7720/manifold/issues/73)) ([426912b](https://github.com/nonchan7720/manifold/commit/426912b13ffea9d58e15a1caeabe6d6f9cb60ebb))


### Miscellaneous

* **deps:** update docker/login-action action to v4.5.2 ([#75](https://github.com/nonchan7720/manifold/issues/75)) ([cb89b19](https://github.com/nonchan7720/manifold/commit/cb89b19c81bb9e1aefe2baefd3f58d3f0d919bdb))
* **deps:** update docker/login-action action to v4.6.0 ([#77](https://github.com/nonchan7720/manifold/issues/77)) ([c10997c](https://github.com/nonchan7720/manifold/commit/c10997c610cf3ef06856991686f164e19a9fbec6))
* **deps:** update github-actions (major) ([#45](https://github.com/nonchan7720/manifold/issues/45)) ([6a2b244](https://github.com/nonchan7720/manifold/commit/6a2b244300e58dde7e1c486e56623435deb8ea89))
* **deps:** update grafana/otel-lgtm docker tag to v0.29.2 ([#26](https://github.com/nonchan7720/manifold/issues/26)) ([db71580](https://github.com/nonchan7720/manifold/commit/db71580c220282decc86b368ccaffabfd39cb89e))
* **deps:** update grafana/otel-lgtm docker tag to v0.30.0 ([#80](https://github.com/nonchan7720/manifold/issues/80)) ([d09f857](https://github.com/nonchan7720/manifold/commit/d09f8574b61c2d779bf036976714f31772f9b43c))

## [1.6.1](https://github.com/nonchan7720/manifold/compare/v1.6.0...v1.6.1) (2026-08-03)


### Bug Fixes

* gofmt and golines ([#71](https://github.com/nonchan7720/manifold/issues/71)) ([e24ad63](https://github.com/nonchan7720/manifold/commit/e24ad63658c7f8eb09caa827a62347f7fc25dffc))

## [1.6.0](https://github.com/nonchan7720/manifold/compare/v1.5.1...v1.6.0) (2026-08-01)


### Features

* resource_link の Content-Type を実体から判定して載せる ([#68](https://github.com/nonchan7720/manifold/issues/68)) ([7a07899](https://github.com/nonchan7720/manifold/commit/7a078996c1910047f129d4dd7f7a64b7717d8cc3))


### Bug Fixes

* **deps:** update github.com/modelcontextprotocol/go-sdk to v1.7.0 ([#66](https://github.com/nonchan7720/manifold/issues/66)) ([0a64ef2](https://github.com/nonchan7720/manifold/commit/0a64ef2e5cefbbb43af0ef7f256cdfd17875300d))

## [1.5.1](https://github.com/nonchan7720/manifold/compare/v1.5.0...v1.5.1) (2026-08-01)


### Bug Fixes

* **ci:** use a defined variable in the MCP Registry transport URL ([#64](https://github.com/nonchan7720/manifold/issues/64)) ([376ab1b](https://github.com/nonchan7720/manifold/commit/376ab1b98f27af64a820c9e66e7d196ee76fd732))

## [1.5.0](https://github.com/nonchan7720/manifold/compare/v1.4.0...v1.5.0) (2026-08-01)


### Features

* **renovate:** auto-merge patch updates when CI passes ([#63](https://github.com/nonchan7720/manifold/issues/63)) ([a1495fa](https://github.com/nonchan7720/manifold/commit/a1495fafa9a8cdfce39c05077ed82b363dfce364))


### Bug Fixes

* **deps:** update all patch updates ([#47](https://github.com/nonchan7720/manifold/issues/47)) ([bf73cb8](https://github.com/nonchan7720/manifold/commit/bf73cb8a8c7028871ca0b08c4ddb551409a8535d))
* **deps:** update go minor dependencies ([#60](https://github.com/nonchan7720/manifold/issues/60)) ([67ea4cc](https://github.com/nonchan7720/manifold/commit/67ea4cccb2403a4e898d800b195f9392cee3537c))
* **deps:** update kin-openapi and grpc to patch security vulnerabilities ([#59](https://github.com/nonchan7720/manifold/issues/59)) ([d87eec1](https://github.com/nonchan7720/manifold/commit/d87eec158031f29e2ae22356a79b1963bfe60873))


### Documentation

* add English README, community health files, and runnable examples ([#57](https://github.com/nonchan7720/manifold/issues/57)) ([cab9292](https://github.com/nonchan7720/manifold/commit/cab9292b8f5a11170171301e942d7aa375992d03))

## [1.4.0](https://github.com/nonchan7720/manifold/compare/v1.3.0...v1.4.0) (2026-07-31)


### Features

* add healthz path ([#55](https://github.com/nonchan7720/manifold/issues/55)) ([69ebbfb](https://github.com/nonchan7720/manifold/commit/69ebbfb1196dfc609c3f429b95b96b6b424fae55))

## [1.3.0](https://github.com/nonchan7720/manifold/compare/v1.2.1...v1.3.0) (2026-07-31)


### Features

* add resource link support ([#30](https://github.com/nonchan7720/manifold/issues/30)) ([e48c0d5](https://github.com/nonchan7720/manifold/commit/e48c0d5a1df80b67470c020cf5ecc95e086fa422))
* alias download handler ([#41](https://github.com/nonchan7720/manifold/issues/41)) ([cb8cc23](https://github.com/nonchan7720/manifold/commit/cb8cc23d230b4dc4c6f4d5b455adb8145ea7e1a7))
* support inmemory ([#53](https://github.com/nonchan7720/manifold/issues/53)) ([c2f734b](https://github.com/nonchan7720/manifold/commit/c2f734bf44e5d633042caa92a24b9b6a470488a7))
* Token Exchange対応とMCPバックエンド認証方式の統合 ([#35](https://github.com/nonchan7720/manifold/issues/35)) ([0df35cd](https://github.com/nonchan7720/manifold/commit/0df35cd086b0bfa3d6eec9d62a25ce2432760bab))


### Bug Fixes

* **deps:** update go-redis to v9.21.0 ([#28](https://github.com/nonchan7720/manifold/issues/28)) ([13ffcbc](https://github.com/nonchan7720/manifold/commit/13ffcbc4831858b2ede00db7d7abef0ac5a17521))
* **deps:** update module github.com/compose-spec/compose-go/v2 to v2.13.0 ([#31](https://github.com/nonchan7720/manifold/issues/31)) ([eed6a5a](https://github.com/nonchan7720/manifold/commit/eed6a5a38513cd8dc4b048db085b9045aba774b1))
* **deps:** update module github.com/getkin/kin-openapi to v0.142.0 ([#29](https://github.com/nonchan7720/manifold/issues/29)) ([c2f754f](https://github.com/nonchan7720/manifold/commit/c2f754fa0b194485e004ff061e08979f5618b372))
* **deps:** update module github.com/modelcontextprotocol/go-sdk to v1.6.1 ([#32](https://github.com/nonchan7720/manifold/issues/32)) ([3671ec1](https://github.com/nonchan7720/manifold/commit/3671ec18344b53e86afbf86de15c93a17a798f0a))
* **deps:** update module modernc.org/sqlite to v1.53.0 ([#36](https://github.com/nonchan7720/manifold/issues/36)) ([f05093d](https://github.com/nonchan7720/manifold/commit/f05093d092b66d9abfba0a9fb15358d17cfb5184))
* **deps:** update opentelemetry ([#37](https://github.com/nonchan7720/manifold/issues/37)) ([4f8a6ca](https://github.com/nonchan7720/manifold/commit/4f8a6caf03b60253201e87363ede8984b6cabd53))
* parameter rename and file upload support and nested parameters ([#21](https://github.com/nonchan7720/manifold/issues/21)) ([5387561](https://github.com/nonchan7720/manifold/commit/5387561059916df7c1b304662138809bc0e3ce9e))


### Miscellaneous

* Configure Renovate ([#23](https://github.com/nonchan7720/manifold/issues/23)) ([c7729fa](https://github.com/nonchan7720/manifold/commit/c7729fa3ee6d235a7ffe331352946d2ce78f806d))
* **deps:** update github-actions (major) ([#38](https://github.com/nonchan7720/manifold/issues/38)) ([86c112f](https://github.com/nonchan7720/manifold/commit/86c112f9149a309e68ebbe636f517507af9e84f2))


### Documentation

* READMEにLiteLLMへのインスピレーションを追記 ([#18](https://github.com/nonchan7720/manifold/issues/18)) ([115547b](https://github.com/nonchan7720/manifold/commit/115547b19486796667f6239e4c93620781575188))
* Update README with new features and configuration options ([#42](https://github.com/nonchan7720/manifold/issues/42)) ([1c405cc](https://github.com/nonchan7720/manifold/commit/1c405ccc6ec26ec397c0e0d3b5409348d9570d73))


### Code Refactoring

* 404 page ([#43](https://github.com/nonchan7720/manifold/issues/43)) ([f1791f1](https://github.com/nonchan7720/manifold/commit/f1791f1d53c3b9a2bdd3cc95ebdb5759bab25aed))

## [1.2.1](https://github.com/nonchan7720/manifold/compare/v1.2.0...v1.2.1) (2026-04-19)


### Bug Fixes

* config ([#17](https://github.com/nonchan7720/manifold/issues/17)) ([17c0f06](https://github.com/nonchan7720/manifold/commit/17c0f06cd172f2c565b49142988f9ca64a17885a))
* issue metadata ([#14](https://github.com/nonchan7720/manifold/issues/14)) ([81574a8](https://github.com/nonchan7720/manifold/commit/81574a89bdb325b65bcc6405051cb7ff170b9073))


### Code Refactoring

* add trace span ([#16](https://github.com/nonchan7720/manifold/issues/16)) ([a420d3a](https://github.com/nonchan7720/manifold/commit/a420d3a1f1240ff4a3257d8a651d5b2f79d0359c))

## [1.2.0](https://github.com/nonchan7720/manifold/compare/v1.1.1...v1.2.0) (2026-04-13)


### Features

* mcp list and support for claude code DCR ([#13](https://github.com/nonchan7720/manifold/issues/13)) ([c317fdc](https://github.com/nonchan7720/manifold/commit/c317fdc62c182ee7007d4519a9c3cdc7a095665a))


### Bug Fixes

* **auth:** fix OAuth2 security vulnerabilities (Critical + High) ([#11](https://github.com/nonchan7720/manifold/issues/11)) ([edff658](https://github.com/nonchan7720/manifold/commit/edff6589169c1683a5b8aea8a3247b66c84a0513))

## [1.1.1](https://github.com/nonchan7720/manifold/compare/v1.1.0...v1.1.1) (2026-04-12)


### Bug Fixes

* auth server oauth2.1 proxy ([#9](https://github.com/nonchan7720/manifold/issues/9)) ([d98e972](https://github.com/nonchan7720/manifold/commit/d98e9725f29b4bc963076ba15376e3137b1d9834))

## [1.1.0](https://github.com/nonchan7720/manifold/compare/v1.0.0...v1.1.0) (2026-04-11)


### Features

* support for sqlite ([#6](https://github.com/nonchan7720/manifold/issues/6)) ([ed1b532](https://github.com/nonchan7720/manifold/commit/ed1b532a4a81e44ad54eadc118f71283daf00f95))


### Bug Fixes

* lint and test ([#3](https://github.com/nonchan7720/manifold/issues/3)) ([5cd6d43](https://github.com/nonchan7720/manifold/commit/5cd6d432c0808d4134a619076bc85f0e94e466c2))


### Documentation

* Update README ([#7](https://github.com/nonchan7720/manifold/issues/7)) ([c75511e](https://github.com/nonchan7720/manifold/commit/c75511e55f1aee72cbdf5f2f1937e22ef7a545df))


### Code Refactoring

* add test code ([#5](https://github.com/nonchan7720/manifold/issues/5)) ([eb1ad0c](https://github.com/nonchan7720/manifold/commit/eb1ad0c120fbf768f0fd529b9ae77b82d60292b6))

## 1.0.0 (2026-04-10)


### Features

* pre release ([#1](https://github.com/nonchan7720/manifold/issues/1)) ([21a8d78](https://github.com/nonchan7720/manifold/commit/21a8d787bd5a7d77dd5518c229a8bdc3e363146a))
