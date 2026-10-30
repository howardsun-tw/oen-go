# Oen Go SDK

An unofficial Go client for the [Oen Tech Payment API](https://documenter.getpostman.com/view/26861354/2sBY4MuLoX)
(應援科技金流). It covers the Payment API endpoints listed in [API coverage](docs/api-coverage.md), plus webhook parsing
and signature verification.
The [`subscription`](#subscription-api) package covers Oen's separate
[Subscription API](https://developer.oen.tw/products/subscription-api/).

Not affiliated with or endorsed by 應援科技.

```
go get github.com/howardsun-tw/oen-go
```

Requires Go 1.23 or later. The SDK has no dependencies outside the standard
library. CI tests the minimum version declared in `go.mod`, Go 1.27, and the
latest stable Go release. Every lane also runs the same independent
[consumer module](integration/consumer), using that toolchain's Go defaults.
The weekly CI schedule detects new stable releases even when no code changes.

## What this SDK is, and is not

It speaks Oen's own vocabulary: one method per documented endpoint, fields
named after Oen's fields, and Oen's response codes preserved. It holds no
state, no schedule and no business rules. Subscription lifecycles,
entitlements, storage, idempotency of your own operations and retry policy stay
in your application, which is the only place that knows what a failed charge
should mean.

## Three rules worth reading before the API

**1. Nothing is ever retried for you.** Payment API writes accept an optional
`IdempotencyKey`; without one, resending can create a second charge. See
[Idempotency keys](#idempotency-keys) for protected resends and their limits. When a state-changing request
does not produce a clear answer — a timeout, a dropped connection, an HTTP
429 or 5xx, a body that is not JSON, or the provider's own `F0001` — the SDK returns
an error that satisfies `errors.Is(err, oen.ErrUnknownOutcome)`. The money may
already have moved. Ask Oen what happened:

```go
result, err := client.ChargeToken(ctx, charge)
switch {
case err == nil:
    // Charged.
case oen.IsDeclined(err):
    // Settled: the money did not move. Show the payer a reason.
case oen.IsUnknownOutcome(err):
    if oen.IsRateLimited(err) {
        // Respect oen.RetryAfter(err) before further requests.
        // The delay does not authorize sending the charge again.
    }
    found, qErr := client.ListOrderTransactions(ctx, charge.OrderID)
    // found tells you whether the charge exists. Never send it again blindly.
}
```

HTTP redirects are not followed, including 307 and 308 redirects that would
replay a charge. They return `ErrUnknownOutcome`. A supplied `HTTPClient` keeps
its original settings; the SDK uses a copy with redirects disabled. Custom
transports must not retry state-changing requests.

Reads (`GetTransaction`, `ListOrderTransactions`, `ListTransactions`,
`GetSubscription`) are safe for *you* to repeat; the SDK still will not do it
on its own.

**2. Verify webhooks, then still confirm them.** Since Payment API v10.3.5
(2026-10-21), Oen signs payment notifications once you generate a signing
secret (`pay_whsec_…`) in the CRM under 總設定 → 開發者 → 應援金流設定. Set
`Config.WebhookSecret` and call `VerifyWebhook` with the raw body and the
request headers:

```go
body, err := io.ReadAll(r.Body) // the raw bytes; re-encoded JSON never matches
if err != nil {
    http.Error(w, "unreadable body", http.StatusBadRequest)
    return
}
event, err := client.VerifyWebhook(body, r.Header)
switch {
case errors.Is(err, oen.ErrInvalidSignature):
    http.Error(w, "invalid signature", http.StatusBadRequest) // forged or stale
    return
case errors.Is(err, oen.ErrInvalidInput):
    // Config.WebhookSecret is not set. Fail so Oen retries once it is fixed.
    http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
    return
case err != nil:
    // Authentic but missing a field Oen documents for its purpose. A resend
    // carries the same body, so keep the raw body for reconciliation and
    // acknowledge only once it is stored.
    if saveUnreadable(body, err) != nil {
        http.Error(w, "store failed", http.StatusServiceUnavailable)
        return
    }
    oen.WriteAck(w)
    return
}
if !event.Purpose.Known() {
    oen.WriteAck(w) // Oen adds purposes; skip them with a 2xx, never an error
    return
}
// event.Verified is true. Store it under event.EventID, then oen.WriteAck(w);
// answer 503 instead if storing fails, so Oen sends it again.
```

It checks `OenPay-Signature` (`t=<unix seconds>,v1=<hex>`, HMAC-SHA256 over
`<t>.<body>`), rejects timestamps more than `Config.WebhookTolerance` (default
5 minutes) from `Config.Now`, and rejects an `OenPay-Event-Id` header that
differs from the event ID recomputed from the body. Without a configured
secret it returns `ErrInvalidInput` instead of passing anything through. A
rotated secret stops verifying immediately; Oen keeps no overlap for the
Payment API. `VerifyWebhookSignature` checks only the signature, for a body
you want to authenticate without parsing.

`EventID` is the deduplication key Oen documents (`evt_…`): a resend keeps it,
and `charging` followed by `charged` for one transaction are two events.

Every documented `purpose` is parsed: `charge`, `token`,
`schedule_subscription` and `subscription_card_changed`, which Oen sends by
default, and `refund`, `subscription_cancelled`, `subscription_retry`,
`invoice_issued` and `invoice_voided`, which Oen sends only after enabling
"Webhook 新版事件" for you. The purpose-specific parts are in
`event.Refund`, `event.Cancellation`, `event.Retry` and `event.Invoice`. On
refund, retry and invoice events `ID` is an event key such as `refund#…`;
the transaction is `ChargeID`. An unknown `purpose` parses with only the
common fields and `Purpose.Known()` false.

Without a secret, use `ParseWebhook`: it reports `Verified: false` and the
callback may be forged. Both methods reject a `merchantId` that names
another merchant and require the fields Oen documents as always present for
the purpose, such as `merchantId` and `id` on a charge and a boolean
`success` on a token callback. Successful token callbacks must also carry a
nonempty string `token`. Missing or malformed fields return
`ErrInvalidRequest`. A `charge` that is still `charging` (a CVS code or ATM
account was just issued) carries no `success`, so `HasOutcome` is false and
`ChargeSucceeded()` is false; status alone is not an outcome flag. `Amount`
is optional on a callback — read it with `HasAmount`. A verified
callback can still be a duplicate or arrive out of order, so before it moves
money-related state, confirm it with `GetTransaction` or
`ListOrderTransactions`.

**3. Card numbers in structured fields do not leave the SDK.** Every payload
the SDK hands back has been through `SanitizeJSON`: PANs under card-number
keys are reduced to their last four digits, and tokens, CVVs and Oen's `sk`
field are replaced with `REDACTED`. `Config.Logf` receives one line per
request — method, path, status, outcome, duration — and never a credential.
Scalar `paymentInfo` is reduced to four characters for card payments and
token callbacks, and for any payload that names no payment method when the
value looks like a card number (13–19 digits passing Luhn); a payload that
names LINE Pay or another non-card method keeps its reference intact.
Free-text fields — `Error.Message`, `WebhookEvent.Message`,
`Transaction.Note`/`Reason`, `Subscription.Note`/`Reason` — are Oen's own
text passed through unmodified, so treat them as untrusted before logging.

## Getting started

```go
client, err := oen.New(oen.Config{
    Environment:       oen.Testing, // or oen.Production
    MerchantID:        "oentech",   // your Oen domain name
    AuthToken:         os.Getenv("OEN_AUTH_TOKEN"),
    Timeout:           10 * time.Second,
    DefaultSuccessURL: "https://merchant.example/paid",
    DefaultFailureURL: "https://merchant.example/failed",
})
```

`Environment` fills in both hosts: the API host and the
`https://{merchantId}.oen.tw` host the payer is redirected to. There is no
default — a client that guessed would send live charges to the wrong place.
Set `BaseURL` (and `CheckoutBaseURL`) instead when you point at a fake.

`AuthToken` comes from the Oen CRM console and differs per environment.

## Timeouts and rate limits

The earliest of `Config.Timeout` (default 10 seconds), the caller's context
deadline and `HTTPClient.Timeout` wins. The deadline covers both receiving
headers and reading the response body. Timeout causes remain available through
`errors.Is(err, context.DeadlineExceeded)`; cancellation remains available
through `errors.Is(err, context.Canceled)`.

HTTP 429 matches `ErrRateLimited` on every endpoint. On POST and PUT it also
matches `ErrUnknownOutcome`; Oen does not document a guarantee that a 429 means
no side effect occurred. Check both helpers, not only `Error.Kind`.
`RetryAfter(err)` reads nonnegative integer seconds or an HTTP date, following
[RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html#name-retry-after).
Oversized valid delays saturate at the maximum `time.Duration`. Missing or
invalid headers return zero; zero is not a signal to retry in a tight loop.
Rate-limit headers survive malformed, oversized or timed-out response bodies.

The SDK does not sleep, throttle or retry automatically. Limit concurrency in
the caller and use bounded backoff with jitter when a read needs another
attempt and no useful `Retry-After` was provided. For writes, reconcile the
outcome first. An empty immediate query is not proof that an earlier request
will never finish; keep reconciliation and any resend decision in the caller.

## Idempotency keys

Payment API support is optional. Existing callers can leave `IdempotencyKey`
empty. To opt in, persist a unique key with the operation before its first
request and set the request field:

```go
charge := oen.TokenChargeRequest{
    IdempotencyKey: "order-A001-charge-1",
    OrderID: "order-A001",
    Token: token,
    Amount: 1000,
    Items: items,
}
result, err := client.ChargeToken(ctx, charge)
```

The field becomes the `Idempotency-Key` header, never a JSON body field.
Nonempty keys must contain 1–255 visible ASCII characters without spaces;
invalid keys fail locally with `ErrInvalidInput`. The SDK never generates,
stores or rotates keys, or retries automatically.

Use the original key, method, path and body for a retry within 24 hours of the
first request. Completed requests return the saved result, including saved
errors. A new operation needs a new key. After 24 hours, Oen treats the key
as new; it no longer protects a retry of the original payment.

A provider `V0001` returned during processing may be saved under the key.
After correcting that rejected request's body, use a new key. A provider
validation failure leaves the key unused only if it occurs before processing. SDK-local
`ErrInvalidInput` failures never send a request and do not consume the key.
For `I0001`, only the current, conflicting request was rejected: the original
request may already have applied. Reconcile it before creating a new operation.

| SDK methods | Protection and recovery |
| --- | --- |
| `ChargeToken`, `SubscribeToken`, `Refund` | Same-key resend protection; query on `I0003` or `C026`. |
| `CreateCheckout`, `CreateSubscriptionCheckout`, `CreateScheduleCheckout`, `CreateTokenCheckout` | Saved responses replay, but if creation completed without saving the response, a same-key resend after 5 minutes can create another resource. Query the transaction/subscription first. Card binding cannot be found by order ID; contact Oen. |
| `CancelSubscription` | Saved responses replay; query the subscription after an uncertain result. |

`/setup3d` and `/check3d` are outside this protection and are not SDK methods.
Query transaction status when their outcome is uncertain.

| Code | SDK classification | Caller action |
| --- | --- | --- |
| `I0001` (`CodeIdempotencyConflict`) | `KindInvalidRequest` | The key was reused with a different request. Reconcile the original operation before deciding on a new one. |
| `I0002` (`CodeIdempotencyInProgress`) | `KindUnknownOutcome` | Still processing; wait before resending the identical request with the same key within its 24-hour window. |
| `I0003` (`CodeIdempotencyUnknownOutcome`) | `KindUnknownOutcome` | Query using `Error.ResourceID` or the order ID; never switch keys to charge again. |
| `C026` (`CodePaymentUnknownOutcome`) | `KindUnknownOutcome` | Query first; never switch keys to charge again. |

For other uncertain writes, reconcile first and apply the endpoint's protection
limits before deciding whether to resend. The SDK has no blanket retry helper
because it cannot know the original key age or whether a checkout was created.

Source: [Oen Payment API — Idempotency-Key](https://developer.oen.tw/api/#idempotency-key),
checked 2026-10-20. The official error table permits a same-key resend for
`C026`; this guide follows the supplied merchant announcement's query-first
procedure. Neither permits a new-key charge to resolve an uncertain payment.

## The token flow

Recurring payment driven by your own scheduler, rather than by Oen's
subscription product, is three steps.

```go
// 1. Bind a card. The token is not in this response.
binding, err := client.CreateTokenCheckout(ctx, oen.TokenCheckoutRequest{
    CustomID: bindingRef, // echoed back on the callback
})
// Redirect the payer to binding.RedirectURL.

// 2. Oen calls your webhook with the token.
event, err := client.VerifyWebhook(body, r.Header)
if event.TokenBound() {
    store(event.CustomID, event.Token, event.PaymentInfo.CardLast4)
}
oen.WriteAck(w)

// 3. Charge the stored token whenever your schedule says so.
result, err := client.ChargeToken(ctx, oen.TokenChargeRequest{
    OrderID: orderRef, // your reference; this is what makes recovery possible
    Token:   token,
    Amount:  1000,
    Items: []oen.LineItem{{
        ProductionCode: "P0001", Description: "monthly plan",
        Quantity: 1, Unit: "個", UnitPrice: 1000,
    }},
})
```

`Token` is populated only on a token callback that succeeded, so a failed
binding can never hand one back. Give every charge attempt its own `OrderID`:
it is the key `ListOrderTransactions` searches by, and therefore the only way
to find out what an uncertain charge did.

Line items must add up to `Amount`. The SDK checks that locally and refuses
with `oen.ErrProductAmountMismatch` rather than spending a provider call on a
request Oen will reject.

## Hosted pages

| Method | Page |
| --- | --- |
| `CreateCheckout` | one payment |
| `CreateSubscriptionCheckout` | recurring payment, charged monthly by Oen |
| `CreateScheduleCheckout` | recurring payment with a start date and interval |
| `CreateTokenCheckout` | card binding, returns a token by webhook |

Each returns a `CheckoutSession` whose `RedirectURL` is where the payer goes.

## Queries, subscriptions and refunds

```go
transaction, err := client.GetTransaction(ctx, transactionHID)
transactions, err := client.ListOrderTransactions(ctx, orderID)
page, err := client.ListTransactions(ctx, oen.ListTransactionsRequest{Page: previous.NextPage})

subscription, err := client.GetSubscription(ctx, subscriptionID)
subscription, err = client.CancelSubscription(ctx, oen.CancelSubscriptionRequest{
    SubscriptionID: subscriptionID, Reason: "客戶自行取消",
})

refunded, err := client.Refund(ctx, oen.RefundRequest{
    TransactionHID: transaction.ID, Amount: 1000, Items: items,
})

// Recurring orders sold through the Oen store (應援商店定期購), which are not
// Payment API subscriptions.
orders, err := client.ListStoreSubscriptions(ctx, oen.ListStoreSubscriptionsRequest{
    Statuses: []oen.StoreSubscriptionStatus{oen.StoreSubscriptionRetryScheduled},
})
```

Oen has two identifiers per transaction: `Transaction.ID` is the HID
(`P20240412QJMAZNML`), which is what refunds take, and `Transaction.TransactionID`
is the other one. `TransactionStatus` answers `IsPending`, `IsPaid`,
`IsFailed` and `IsRefunded`; a status this SDK version does not know answers
false to all of them and `Known()` reports false, so a new provider state can
never be read as a settled outcome.

## Subscription API

Oen's Subscription API is a separate product: products and plans you define,
one shared subscription page, trials, plan changes, end-of-period
cancellation and signed webhooks. It has its own host
(`https://subscription-api.oen.tw`, production only), its own `sub_sk_` keys
with scopes, and its own error format, so it lives in its own package with its
own `Client`.

```go
import "github.com/howardsun-tw/oen-go/subscription"

client, err := subscription.New(subscription.Config{
    APIKey: os.Getenv("OEN_SUBSCRIPTION_API_KEY"), // sub_sk_…
})

sub, err := client.GetSubscription(ctx, "sub_…")
if sub.Status == subscription.StatusPaused {
    link, err := client.CreateRecoveryLink(ctx, sub.ID)
}

result, err := client.CancelSubscription(ctx, subscription.KeyedRequest{
    SubscriptionID: sub.ID,
    IdempotencyKey: cancelKey, // store it before sending
})
if subscription.RetryableWithSameKey(err) {
    // Oen returns the original result for a resend with the same key.
}
```

What differs from the Payment API:

- **Required idempotency keys.** Unlike Payment API optional keys, plan change, period change, cancel, resume, terminate,
  refund and webhook resend require `IdempotencyKey`; the SDK refuses to send
  them without one. After a timeout, a transport failure or a 5xx other than
  `SA027`, `RetryableWithSameKey(err)` is true and the same key may be resent. Product, plan and customer writes and
  recovery links take no key: an unknown outcome there may mean the write
  applied.
- **Errors** are `*subscription.Error` with Oen's `Errno` (`SA002`, `X023`, …).
  `Kind` follows the HTTP status, except where Oen's meaning differs: `SA015`
  and `SA025` are `KindInProgress` (query, do not resend), `SA027` is
  `KindDeclined`, `X016` is `KindConflict`, and `M012` is `KindForbidden`.
  `(*Error).Description()` and `subscription.LookupErrno(errno)` explain each
  errno.
- **Webhooks are signed.** `WebhookVerifier{Secret: "sub_whsec_…"}.Verify(body,
  r.Header)` checks `OenPay-Signature` against the raw body, accepts either
  signature during a secret rotation, and rejects timestamps more than 5
  minutes from the local clock. Deduplicate by `Event.ID`.
- **Undocumented fields stay raw.** Oen documents few response fields. Typed
  fields are `Subscription.ID`, `Status` and `NextChargeAt`, every resource's
  `ID`, `Page.Next`, and the webhook's subscription and payment detail.
  Everything else is in a sanitized `Raw` or `Result.Data`. Request bodies Oen
  does not document (plan change, period change, refund, updates) are a
  `Fields` map you build.

There is no testing environment for the Subscription API, so this package has
been tested only against local fakes. See
[`docs/api-coverage.md`](docs/api-coverage.md#subscription-api).

## Errors

Every provider failure is an `*oen.Error` with the operation, Oen's code, its
message, the HTTP status and a `Kind`.

| Helper | Kind | What it means |
| --- | --- | --- |
| `IsDeclined` | `KindDeclined` | Settled refusal (`T0001`–`T0005`, `T0009`, `T0010`, `K0001`). Nothing moved. |
| `IsInvalidRequest` | `KindInvalidRequest` | `V0001` (or another V code except `V0002`), or `I0001`. For `I0001`, reconcile the original operation; it may already have applied. For provider `V0001`, a corrected body needs a new key because the error may be saved. |
| `IsUnauthorized` | `KindUnauthorized` | `A0001`, `A0002` (token expired), or another A code. |
| — | `KindTransactionState` | `V0002`: the target is not in a state that allows this. |
| `IsRateLimited` | `KindRateLimited` | HTTP 429. Wait `RetryAfter(err)`. |
| `IsUnknownOutcome` | `KindUnknownOutcome`, and also POST/PUT 429 | Query before doing anything else. A rate-limited write keeps `KindRateLimited` and still matches this helper. |
| — | `KindInvalidInput` | An `*oen.ValidationError`: the request never left. |

`(*Error).DeclineReason()` maps a decline onto `insufficient_funds`,
`card_expired`, `card_declined`, `invalid_cvv` or `transaction_failed`.

`(*Error).Description()` explains the code in English. `oen.LookupCode(code)`
also returns the documented HTTP status, the `Kind`, Oen's own meaning in
Traditional Chinese and the recommended action:

```go
var oenErr *oen.Error
if errors.As(err, &oenErr) {
    if info, ok := oen.LookupCode(oenErr.Code); ok {
        log.Printf("%s %s: %s (%s). Next: %s",
            oenErr.Op, info.Code, info.Description, info.OfficialDescription, info.Action)
    }
}
```

`LookupCode` covers the `code` field of API responses only. It does not
describe the `payment_error` parameter on a checkout `failureUrl`, where
`V0003` means "result pending". `K0001` (risk control) is a decline; `R0001`
(refund failed) is an unknown outcome, so query the transaction first.

HTTP 400 or 401 without a usable provider code is `KindUnknownOutcome`.
Response decoding failures retain the HTTP status and any decoded provider code.
Malformed monetary values never become zero: transaction and subscription query
`amount` is required, while absent or null `fee` and `refundAmount` default to
zero. Oen's cancellation response may omit `amount`; check
`Subscription.HasAmount` before using that value. Supplied values that are not
whole `int64` amounts, integers outside the platform range, or timestamps that
are present but unreadable return `ErrUnknownOutcome` (or `ErrInvalidRequest`
when parsing a webhook); absent or null timestamps are the zero time.

Local validation failures are `*oen.ValidationError` values that match
`errors.Is(err, oen.ErrInvalidInput)` and name the offending field. They never
match `ErrDeclined` or `ErrUnknownOutcome`, because no request was sent.

## Testing against the fake

`oentest` is a programmable fake of the whole API, and the SDK's own tests run
against it.

```go
server := oentest.New()
defer server.Close()

server.Reject("/token/transactions", oen.CodeInsufficientFunds, "額度不足")
server.FailHTTP("/transactions/{id}", http.StatusInternalServerError)
server.Hang("/token/transactions")                                  // times out
server.ChargeThenFail("/token/transactions", http.StatusGatewayTimeout) // took the money, told you nothing
```

`ChargeThenFail` is the one worth knowing: it reproduces the outcome a charge
cannot tell apart from a charge that never happened, so you can test that your
recovery queries instead of retrying.

## Timestamps

Oen documents every date field as an ISO date in UTC+0. Values that carry an
offset are read as sent. A value without one is undocumented; `NaiveTimeLocation`
decides how to read it and defaults to `time.UTC`. Set it to `Asia/Taipei` if
you have evidence that Oen sends local times.

## Compatibility

The module follows semantic versioning from its first tagged release. Until
`v1.0.0`, exported names may change; the safety rules above will not.

The SDK's `go.mod` declares the minimum supported version, not a maximum or a
toolchain pin. The `stable` CI lane automatically follows new stable releases;
a future release is verified only once those checks pass. Go 1.27 stays in the
matrix as an explicit compatibility target, without any version-specific SDK
implementation or duplicate test suite.

Run `sh scripts/test-consumer.sh` with the desired `go` binary on `PATH` to
verify the consumer independently. The runner sets a temporary module's `go`
directive to the active compiler version and leaves the checked-in modules
unchanged.

See [docs/api-coverage.md](docs/api-coverage.md) for what is implemented, what
is not, and what has not been verified against a live provider.

## License

MIT. See [LICENSE](LICENSE).

## Development toolchain

Use Go 1.27.3 for development (`.go-version`) and the pinned Go 1.27 CI lane.
Both modules retain their Go 1.23 minimum; CI still checks the minimum and latest stable Go.
