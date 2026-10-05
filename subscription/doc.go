// Package subscription is an unofficial Go client for the Oen Tech
// Subscription API (應援科技 Subscription API), documented at
// https://developer.oen.tw/products/subscription-api/.
//
// The Subscription API is a separate product from the Payment API in the
// parent package: it has its own host, its own sub_sk_ keys, its own error
// format, Idempotency-Key support and signed webhooks. This package does not
// share configuration or error types with package oen.
//
// # Documented and undocumented fields
//
// Oen documents the endpoints, the request fields for creating products and
// plans, the error codes, a few response fields and the webhook signature.
// Those are Go types here. Response objects carry their whole sanitized
// payload in a Raw field, and request bodies whose fields are not documented
// are passed through as a caller-built map. The package never guesses a field
// name.
//
// # Safety rules
//
// No method retries. Seven operations require an Idempotency-Key: changing a
// plan or a period, cancelling, resuming, terminating, refunding and resending
// a webhook delivery. Oen documents that resending one of them with the same
// key returns the original result, so after [ErrUnknownOutcome] the caller may
// resend with the same key; see [RetryableWithSameKey]. Every other write has
// no key, and an unknown outcome there can mean the write applied.
//
// Webhooks are signed. [WebhookVerifier] checks the OenPay-Signature header
// against the raw body before it decodes anything.
package subscription
