# Oen Go SDK

An unofficial Go client for the [Oen Tech Payment API](https://documenter.getpostman.com/view/26861354/2sBY4MuLoX)
(應援科技金流). It covers every endpoint Oen documents, plus webhook parsing.

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

**1. Nothing is ever retried for you.** Oen offers no idempotency key on any
endpoint, so a resent charge is a second charge. When a state-changing request
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

**2. A webhook is a claim, not a proof.** Oen publishes no webhook signature,
so `ParseWebhook` decodes and sanitizes the payload and reports
`Verified: false` every time. It checks that the callback names your merchant
and requires nonempty string `merchantId` and `id`, a supported `purpose`
(`charge` or `token`), and a boolean `success`. Successful token callbacks must
also carry a nonempty string `token`. Missing or malformed fields return
`ErrInvalidRequest`; status alone is not an outcome flag. `Amount` is
optional on a callback — read it with `HasAmount`. Before a callback moves
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
event, err := client.ParseWebhook(body)
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
```

Oen has two identifiers per transaction: `Transaction.ID` is the HID
(`P20240412QJMAZNML`), which is what refunds take, and `Transaction.TransactionID`
is the other one. `TransactionStatus` answers `IsPending`, `IsPaid`,
`IsFailed` and `IsRefunded`; a status this SDK version does not know answers
false to all of them and `Known()` reports false, so a new provider state can
never be read as a settled outcome.

## Errors

Every provider failure is an `*oen.Error` with the operation, Oen's code, its
message, the HTTP status and a `Kind`.

| Helper | Kind | What it means |
| --- | --- | --- |
| `IsDeclined` | `KindDeclined` | Settled refusal (`T0001`–`T0005`). Nothing moved. |
| `IsInvalidRequest` | `KindInvalidRequest` | `V0001` (or another V code except `V0002`). Fix the request. |
| `IsUnauthorized` | `KindUnauthorized` | `A0001` (or another A code). |
| — | `KindTransactionState` | `V0002`: the target is not in a state that allows this. |
| `IsRateLimited` | `KindRateLimited` | HTTP 429. Wait `RetryAfter(err)`. |
| `IsUnknownOutcome` | `KindUnknownOutcome` | Query before doing anything else. |
| — | `KindInvalidInput` | An `*oen.ValidationError`: the request never left. |

`(*Error).DeclineReason()` maps a decline onto `insufficient_funds`,
`card_expired`, `card_declined`, `invalid_cvv` or `transaction_failed`.

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
