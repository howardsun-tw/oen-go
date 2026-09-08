# API coverage

What this SDK implements, measured against Oen's own documentation.

## Sources

| Source | Contents | Used for |
| --- | --- | --- |
| [Oen Tech Payment API (全幕前交易)](https://documenter.getpostman.com/view/26861354/2sBY4MuLoX) | 12 endpoints including the token endpoints, the webhook table, the transaction and subscription resources, and the response code table | The contract this SDK implements |
| [Oen Tech Payment API](https://documenter.getpostman.com/view/26859697/2s9YsQ7VJA) | 9 endpoints; identical apart from the three token endpoints, which it does not list | Cross-check only |

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

Every request carries `Content-Type: application/json` and
`Authorization: Bearer {authToken}`. Every response is
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
| Webhook callback | (merchant endpoint) | `ParseWebhook`, `WriteAck` | yes |

Every documented endpoint is implemented. The SDK adds no endpoint that Oen
does not document.

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
| Webhook signature verification | Oen publishes no signature. `WebhookEvent.Verified` is always false. |
| Card-binding lookup | Oen has no endpoint that returns a binding by its `/checkout-token` session or `customId` (confirmed with Oen on 2026-09-05; they were building one). Confirm before assuming it exists. |
| Idempotency keys | Not offered on any endpoint. This is why no request is ever retried automatically. |
| Currencies other than TWD | Oen documents `TWD` only; the SDK refuses anything else locally. |
| Invoice queries, payout queries, CRM operations | Not in either collection. |

## Not verified against a live provider

All 12 HTTP endpoints have been exercised with the official response snapshot
and `oentest`. No endpoint has been run against Oen's sandbox in this work.
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
  same public SDK/fake-server tests cover all 12 endpoints, timeout and rate
  limits on every version. There is no Go-version-specific SDK code.
- Every endpoint is tested for the SDK timeout, caller deadline and injected
  HTTP client timeout, both before headers and while reading a stalled body.
- Every endpoint is tested for cancellation before sending and for 429 with
  normal, empty, HTML, oversized and stalled bodies, without a second request.
- `Retry-After` tests cover integer seconds, HTTP dates, missing/invalid values
  and overflow. Timeouts and rate-limited writes also have charge-then-query
  recovery tests.

Local verification on 2026-09-08 (darwin/arm64): Go 1.23.12 and Go 1.27.0 each
passed build, vet, 425 SDK test items with the race detector, and 4 consumer
test items, with zero skipped tests. Go 1.27 statement coverage across the SDK
and fake was 95.4%. These are local results; hosted CI and Oen sandbox execution
are separate validation steps.
