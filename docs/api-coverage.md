# API coverage

What this SDK implements, measured against Oen's own documentation.

## Sources

| Source | Contents | Used for |
| --- | --- | --- |
| [Oen Tech Payment API (全幕前交易)](https://documenter.getpostman.com/view/26861354/2sBY4MuLoX) | 12 endpoints including the token endpoints, the webhook table, the transaction and subscription resources, and the response code table | The contract this SDK implements |
| [Oen Tech Payment API](https://documenter.getpostman.com/view/26859697/2s9YsQ7VJA) | 9 endpoints; identical apart from the three token endpoints, which it does not list | Cross-check only |
| [應援金流文件 — 查詢商店定期購訂單列表](https://developer.oen.tw/api/list-subscriptions/) | `GET /subscriptions`: query parameters, response field table and error codes; no response example | `ListStoreSubscriptions` (retrieved 2026-10-05) |

Both collections were read on 2026-09-08 through the Postman documenter API.
The only difference found is coverage: `POST /checkout-token`,
`POST /token/transactions` and `POST /token/subscriptions` appear in the first
collection and not in the second. Everything present in both matches. Earlier
notes that the token endpoints were undocumented were based on the second
collection alone.

Hosts, from the collection description:

| Environment | API | Hosted pages |
| --- | --- | --- |
| Production | `https://payment-api.oen.tw` | `https://{merchantId}.oen.tw` |
| Testing | `https://payment-api.testing.oen.tw` | `https://{merchantId}.testing.oen.tw` |

Every request carries `Authorization: Bearer {authToken}`. Requests that
carry a body also send `Content-Type: application/json`. Every response is
`{"code":"","message":"","data":{}}`.

Hosted page redirects, from each endpoint's description in the collection
("取得 Response 後 轉址至"), all on the hosted-page host above:

| Endpoint | Payer is sent to |
| --- | --- |
| `POST /checkout` | `/checkout/{data.id}` |
| `POST /checkout-subscription` | `/checkout/subscription/{data.id}` |
| `POST /checkout-schedule` | `/checkout/schedule/{data.id}` |
| `POST /checkout-token` | `/checkout/subscription/create/{data.id}` |

## Endpoints

| Operation | Endpoint | SDK | Used by hyper `howard/xsy-2003-billing-schema` |
| --- | --- | --- | --- |
| 取得單次交易頁面 | `POST /checkout` | `CreateCheckout` | yes |
| 取得定期定額交易頁面 | `POST /checkout-subscription` | `CreateSubscriptionCheckout` | no |
| 取得設定未來交易之定期定額交易頁面 | `POST /checkout-schedule` | `CreateScheduleCheckout` | no |
| 用卡號透過 3D 驗證取得 token | `POST /checkout-token` | `CreateTokenCheckout` | yes |
| 用 Token 發起單筆交易 | `POST /token/transactions` | `ChargeToken` | yes |
| 用 Token 發起定期定額交易 | `POST /token/subscriptions` | `SubscribeToken` | no |
| 查詢交易明細 | `GET /transactions/:id` | `GetTransaction` | yes |
| 用訂單編號查詢交易列表 | `GET /order/:orderId/transactions` | `ListOrderTransactions` | yes |
| 查詢交易列表 | `GET /transactions?page&start&end` | `ListTransactions` | no |
| 查詢定期定額明細 | `GET /subscriptions/:subscriptionId` | `GetSubscription` | no |
| 取消定期定額 | `PUT /subscriptions/:subscriptionId` | `CancelSubscription` | no |
| 退款 | `POST /refunds/:transactionHid` | `Refund` | no |
| 查詢商店定期購訂單列表 | `GET /subscriptions?status&page` | `ListStoreSubscriptions` | no |
| Webhook callback | (merchant endpoint) | `ParseWebhook`, `WriteAck` | yes |

Every documented endpoint is implemented. The SDK adds no endpoint that Oen
does not document.

`GET /subscriptions` is documented only on developer.oen.tw, not in either
Postman collection. It lists recurring orders sold through the Oen store
(應援商店定期購), not Payment API subscriptions. Every row field is optional in
Oen's table; `subscriptions` and `page` are always present. The SDK treats a
missing or non-array `subscriptions` as `ErrUnknownOutcome`, refuses status
values outside `ongoing`, `retryScheduled`, `error`, `cancelled` and `done`
locally, and keeps `items` as sanitized raw JSON because its elements are not
described. With no published response example, its tests use a response
built from the field table; the official-snapshot test counts it separately.

Rechecked both collections on 2026-09-08: the newer collection has 12 endpoints,
the older has 9, and their union has 12. The versioned snapshot in
[`testdata/oen-api-contract.json`](../testdata/oen-api-contract.json) records the
source, retrieval date, collection SHA-256, and all 13 distinct response examples.
`TestEveryOfficialEndpointAcceptsItsPublishedResponses` executes every endpoint
through the public client and checks its method, path, authentication and
acceptance of those official responses.

The resource table says subscription `amount` is required, but the official
`PUT /subscriptions/:subscriptionId` success example omits it. The SDK follows
the endpoint's concrete example for cancellation and reports `HasAmount=false`;
subscription queries still require amount. This documentation conflict should
be clarified with Oen rather than replacing the omitted value with an assumed
known amount.

## Request fields

Optional fields are sent only when set, because Oen omits absent keys from its
own responses and sending an empty string is not the same as sending nothing.

| Endpoint | Sent by the SDK | Not sent |
| --- | --- | --- |
| `POST /checkout` | `merchantId`, `amount`, `currency`, `orderId`, `successUrl`, `failureUrl`, `productDetails`, `userName`, `userEmail`, `userId`, `allowedPaymentMethods`, `use3d`, `customId`, `note`, `expectedPayoutDate` | — |
| `POST /checkout-subscription` | as above minus `allowedPaymentMethods` and `expectedPayoutDate`, plus `numberOfPeriods` | — |
| `POST /checkout-schedule` | as `/checkout-subscription`, plus `paymentInterval`, `startDate` | — |
| `POST /checkout-token` | `merchantId`, `successUrl`, `failureUrl`, `customId`, `note` | — |
| `POST /token/transactions` | `merchantId`, `amount`, `currency`, `orderId`, `token`, `productDetails`, `invoiceInfo`, `userName`, `userEmail`, `userId`, `note`, `expectedPayoutDate` | — |
| `POST /token/subscriptions` | as above plus `numberOfPeriods`, `paymentInterval`, `startDate`; no `expectedPayoutDate` (not documented for this endpoint) | — |
| `PUT /subscriptions/:id` | `merchantId`, `reason` | — |
| `POST /refunds/:hid` | `merchantId`, `amount`, `productDetails`, `remitInfo`, `reason` | — |

## Response codes

| Code | Meaning | SDK kind | May a caller resend? |
| --- | --- | --- | --- |
| `S0000` | 執行成功 | — | — |
| `A0001` | 未授權 | `KindUnauthorized` | Not until the credentials change |
| `V0001` | 請求錯誤 | `KindInvalidRequest` | Not until the request changes |
| `V0002` | 交易狀態錯誤 | `KindTransactionState` | Not until the target's state changes |
| `T0001` | 交易失敗 | `KindDeclined` | No |
| `T0002` | 安全碼 CVV 錯誤 | `KindDeclined` | No |
| `T0003` | 卡片過期 | `KindDeclined` | No |
| `T0004` | 額度不足 | `KindDeclined` | No |
| `T0005` | 拒絕授權 | `KindDeclined` | No |
| `F0001` | 系統錯誤 | `KindUnknownOutcome` | Only after querying |
| Undocumented `T*`/`V*`/`A*` | — | By first letter | As for the letter |
| Any other undocumented code | — | `KindUnknownOutcome` | Only after querying |
| HTTP 429 on GET | Not documented by Oen | `KindRateLimited` | Read may be repeated after `RetryAfter` or bounded backoff |
| HTTP 429 on POST/PUT | No guarantee of nonexecution documented | `KindRateLimited`, also matches `ErrUnknownOutcome` | Only after reconciling; delay alone is not permission to resend |
| HTTP 5xx, timeout, transport failure, unreadable body | — | `KindUnknownOutcome` | Only after querying |

## Parsing validation

- Webhook identity and outcome fields are required: nonempty string
  `merchantId` matching the client, nonempty string `id`, `purpose` equal to
  `charge` or `token`, and boolean `success`. A successful token callback must
  carry a nonempty string `token`. Parsing still does not authenticate it.
- Transaction and subscription query `amount` is required. Cancellation may
  omit it and sets `Subscription.HasAmount=false`; a webhook may omit it and
  sets `WebhookEvent.HasAmount=false`. Supplied monetary fields must parse as
  whole `int64` amounts; malformed values are errors, never implicit zeroes.
  Missing or null optional amounts remain zero. Optional integers (`period`,
  `numberOfPeriods`, `quantity`) use the same grammar as amounts and must
  also fit the platform `int`.
- Timestamps that are present but unreadable are errors on every resource
  and on webhooks; absent or null timestamps are the zero time. A bare epoch
  is read only when it arrives as a JSON number, never from a digit string.
- Scalar `paymentInfo` is redacted before building returned DTOs when the
  payload names a card payment or token binding, or names no payment method
  and the value looks like a card number (13–19 digits passing Luhn); LINE
  Pay and other explicitly non-card references remain intact. Inside a
  `paymentInfo` object or array in those contexts, any scalar that looks like
  a card number is reduced to its last four digits regardless of its key.
- Free-text fields (`message`, `note`, `reason`) are passed through
  unmodified; the SDK does not mask card numbers embedded in prose.
- Single resources are read directly from `data`; the `{"transaction":{}}` /
  `{"subscription":{}}` wrapper is unwrapped only for the endpoint's own kind
  and only when the outer object carries no `id`.
- Transaction lists: a bare array, a known list key (`transactions`, `items`,
  `data`) that is empty or null, or an object with nothing but `page` is an
  empty list; any other object shape is `ErrUnknownOutcome`. The published
  examples show only non-empty lists, so the empty shapes are an assumption.
  A supplied non-null `page` must be a string; malformed tokens return
  `ErrUnknownOutcome` instead of silently ending pagination. String tokens
  are preserved verbatim.
- `Config.BaseURL` and `Config.CheckoutBaseURL` may include a path prefix,
  but cannot contain a query string or fragment: endpoint paths are appended
  to these values. Return URLs may still contain queries and fragments.
- Return URLs (per request and `Config.DefaultSuccessURL`/`DefaultFailureURL`)
  must be absolute `http` or `https` URLs and are refused locally otherwise.
- Response parsing errors preserve the HTTP status and decoded provider code.

## Not implemented, and why

| Capability | Status |
| --- | --- |
| Webhook signature verification | The Payment API publishes no signature (the Subscription API does; see below). `WebhookEvent.Verified` is always false. |
| Card-binding lookup | Oen has no endpoint that returns a binding by its `/checkout-token` session or `customId` (confirmed with Oen on 2026-09-05; they were building one). Confirm before assuming it exists. |
| Idempotency keys | Not offered on any Payment API endpoint. This is why no request is ever retried automatically. |
| Currencies other than TWD | Oen documents `TWD` only; the SDK refuses anything else locally. |
| Invoice queries, payout queries, CRM operations | Not in either collection. |

## Not verified against a live provider

All 13 HTTP endpoints have been exercised with `oentest`, and the 12 in the
Postman collections also with the official response snapshot. No endpoint has been run against Oen's sandbox in this work.
These provider behaviors remain unverified:

- The `page` token's semantics beyond "pass the previous response's value
  back". Oen documents 50 rows per page and states that offset paging is not
  available; the token in its example decodes to a DynamoDB key with a limit
  of 100, so the page size is not something to depend on.
- `start` and `end` on the transaction list: named in the URL, never described.
- Whether Oen ever answers HTTP 429. The rate-limit handling is defensive.
- Timestamps without a UTC offset. Oen documents every date field as ISO in
  UTC+0, and every example carries `Z`. `Config.NaiveTimeLocation` decides how
  an offset-less value is read, and defaults to UTC.

## Go compatibility and failure-path checks

- CI checks the minimum version from `go.mod`, the explicitly supported Go 1.27,
  and `stable`. A weekly schedule checks future stable releases without a code
  change. Each lane runs build, vet and race tests.
- `integration/consumer` is the shared independent consumer module. Its runner
  uses a temporary module with the selected toolchain's `go` directive; the
  same public SDK/fake-server tests cover all 13 Payment API endpoints and all
  23 Subscription API endpoints, the Subscription API webhook signature, and
  timeout and rate limits for both clients on every version. There is no
  Go-version-specific SDK code.
- Every endpoint is tested for the SDK timeout, caller deadline and injected
  HTTP client timeout, both before headers and while reading a stalled body.
- Every endpoint is tested for cancellation before sending and for 429 with
  normal, empty, HTML, oversized and stalled bodies, without a second request.
- Transport rules shared by both clients (no redirects, response size limit,
  `Retry-After` parsing, base URL checks) live in `internal/httpx`.
- `Retry-After` tests cover integer seconds, HTTP dates, missing/invalid values
  and overflow. Timeouts and rate-limited writes also have charge-then-query
  recovery tests.

Local verification on 2026-09-08 (darwin/arm64): Go 1.23.12 and Go 1.27.0 each
passed build, vet, 425 SDK test items with the race detector, and 4 consumer
test items, with zero skipped tests. Go 1.27 statement coverage across the SDK
and fake was 95.4%. These are local results; hosted CI and Oen sandbox execution
are separate validation steps.

## Subscription API

Package `subscription` implements Oen's separate Subscription API.

### Source

[Subscription API](https://developer.oen.tw/products/subscription-api/),
retrieved 2026-10-05 (page last updated 2026-09-29). The same text is the
Subscription API section of `https://developer.oen.tw/llms-full.txt`. It is the
only source: Oen publishes no per-endpoint reference, OpenAPI file or response
examples for this product.

Host `https://subscription-api.oen.tw`, production only. Every path starts with
`/v1`. Every request carries `Authorization: Bearer sub_sk_…`. Success is HTTP
200 with `{"data": …, "paging"?: {"next": …}}`; an error is
`{"errno": "…", "message": "…"}` with a status that depends on the error.

### Endpoints

| Method | Path | Scope | SDK | Idempotency-Key |
| --- | --- | --- | --- | --- |
| `POST` | `/v1/products` | `write:product` | `CreateProduct` | — |
| `GET` | `/v1/products` | `read:*` | `ListProducts` | — |
| `GET` | `/v1/products/{productId}` | `read:*` | `GetProduct` | — |
| `PUT` | `/v1/products/{productId}` | `write:product` | `UpdateProduct` | — |
| `GET` | `/v1/products/{productId}/subscription-url` | `read:*` | `GetSubscriptionURL` | — |
| `POST` | `/v1/products/{productId}/plans` | `write:plan` | `CreatePlan` | — |
| `GET` | `/v1/products/{productId}/plans` | `read:*` | `ListPlans` | — |
| `PUT` | `/v1/products/{productId}/plans/{planId}` | `write:plan` | `UpdatePlan` | — |
| `GET` | `/v1/products/{productId}/subscriptions` | `read:*` | `ListProductSubscriptions` | — |
| `GET` | `/v1/subscriptions/{id}` | `read:*` | `GetSubscription` | — |
| `POST` | `/v1/subscriptions/{id}/plan-change` | `write:subscription` | `ChangePlan` | required |
| `POST` | `/v1/subscriptions/{id}/period-change` | `write:subscription` | `ChangePeriod` | required |
| `POST` | `/v1/subscriptions/{id}/cancel` | `write:subscription` | `CancelSubscription` | required |
| `POST` | `/v1/subscriptions/{id}/resume` | `write:subscription` | `ResumeSubscription` | required |
| `POST` | `/v1/subscriptions/{id}/terminate` | `write:subscription` | `TerminateSubscription` | required |
| `POST` | `/v1/subscriptions/{id}/refunds` | `write:subscription` | `RefundSubscription` | required |
| `POST` | `/v1/subscriptions/{id}/recovery-links` | `write:subscription` | `CreateRecoveryLink` | — |
| `GET` | `/v1/customers` | `read:*` | `ListCustomers` | — |
| `GET` | `/v1/customers/{customerId}` | `read:*` | `GetCustomer` | — |
| `PUT` | `/v1/customers/{customerId}` | `write:customer` | `UpdateCustomer` | — |
| `GET` | `/v1/webhook-deliveries` | `ops:webhook` | `ListWebhookDeliveries` | — |
| `GET` | `/v1/webhook-deliveries/{deliveryId}` | `ops:webhook` | `GetWebhookDelivery` | — |
| `POST` | `/v1/webhook-deliveries/{deliveryId}/resend` | `ops:webhook` | `ResendWebhookDelivery` | required |
| — | merchant webhook endpoint | — | `WebhookVerifier.Verify` | — |

All 23 documented endpoints are implemented.

### What is typed and what is raw

| Documented by Oen | SDK |
| --- | --- |
| Create product fields (`name` ≤ 50, `subscriptionType`, `status`, `summary` ≤ 100, `description`, `trialReuse`, `gracePeriodDays`, `basicInfoFields`, https `websiteUrl`/`successRedirectUrl`/`failureRedirectUrl`, `customerServicePhone`, `customerServiceEmail`) | `CreateProductRequest`, validated locally. `basicInfoFields` has no documented shape and is sent verbatim. |
| Create plan fields (`name` 1–30, `price` ≥ 0, `billingPeriod{unit,interval}`, `trialDays`, `description` ≤ 1000, `status`) | `CreatePlanRequest`, validated locally |
| Subscription `id`, `status` (6 values), `nextChargeAt` | `Subscription` fields |
| ID prefixes `sub_`, `prod_`, `plan_`, `cus_`, `evt_` | `ID` on every resource, read from `id` |
| `paging.next` | `Page.Next` |
| Webhook `id`, `type`, `created`, `data.subscription.{id,status}`, `data.paymentDetail.{amount,currency}`, 17 event types, signature | `Event`, `EventType`, `WebhookVerifier` |

Not documented, therefore raw: every other response field (`Raw` or
`Result.Data`, sanitized by `oen.SanitizeJSON`), and the request bodies of
product, plan and customer updates, plan change, period change, refund and
cancel/resume/terminate (`Fields map[string]any`; `nil` sends `{}`).

Character limits count Unicode characters, not bytes.

### Assumptions to confirm with Oen

- `nextChargeAt` format: accepted as an RFC 3339 string or whole Unix seconds
  (the webhook's `created` format). Anything else is an error.
- Resource responses are a JSON object with `id` at the top level. If Oen nests
  the subscription inside `data`, `Subscription.ID` is empty and the data is
  still in `Raw`.
- Lists return `data` as an array.
- `POST` endpoints without a documented body (recovery links, resend) are sent
  `{}`.
- HTTP 429 is not documented; it is handled as on the Payment API.

### Error classification

`Kind` follows the HTTP status: 400 and 422 `KindInvalidRequest`, 401
`KindUnauthorized`, 403 `KindForbidden`, 404 `KindNotFound`, 409
`KindConflict`, 5xx `KindUnknownOutcome`. These error numbers override it:

| Errno | HTTP | Kind | Why |
| --- | --- | --- | --- |
| `X016` | 400 | `KindConflict` | Reports the subscription's state, not a malformed request |
| `SA015` | 409 | `KindInProgress` | Charge pending or undetermined; Oen says query, do not resend |
| `SA025` | 409 | `KindInProgress` | Refund still processing |
| `SA027` | 502 | `KindDeclined` | Processor refused the refund; confirm, then use a new key |

A 4xx without `errno`, a redirect, a timeout, a transport failure, an
unreadable body or a 2xx body without a `data` key is `KindUnknownOutcome`.
An explicit `"data": null` is a success.

Go's `net/http` replays a request that carries an `Idempotency-Key` header
when a reused connection fails. The SDK clears `Request.GetBody` so that
replay cannot happen and every call sends at most one request;
`TestKeyedWriteIsNotReplayedByTheTransport` reproduces the replay. `RetryableWithSameKey` is true for an
unknown outcome or a rate limit on a request that carried an Idempotency-Key,
including `SA010` and `SA028`, which Oen documents as same-key retries.

### Webhook signature

`OenPay-Signature: t=<unix seconds>,v1=<hex>[,v1=<hex>]`, HMAC-SHA256 under the
`sub_whsec_` secret over `{t}.{raw body}`. Any matching `v1` passes, which
covers the 24-hour rotation window. Oen sets no time limit and recommends 5
minutes; `WebhookVerifier.Tolerance` defaults to that and applies in both
directions. The documentation's local test example signs a
`payment_intent.succeeded` body copied from Embed; that type is not a
Subscription API event, and the SDK passes unknown types through unchanged.

### Not verified against a live provider

Oen has no Subscription API testing environment. Every endpoint has been
exercised only against `httptest` fakes, in the package tests and in the
consumer module, for method, path, query, headers,
body, Idempotency-Key, rate limits, 5xx, redirects, timeouts, transport
failures and malformed bodies. The assumptions above remain unverified.
