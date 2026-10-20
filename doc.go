// Package oen is an unofficial Go SDK for the Oen Tech Payment API
// (應援科技金流).
//
// The SDK speaks Oen's own vocabulary: requests and responses mirror the
// fields in Oen's official documentation, and nothing here knows about
// subscriptions state machines, entitlements or storage. Callers keep those
// concerns and map the SDK types onto their own domain.
//
// # Safety rules
//
// The client never retries a request on its own. A POST that creates a
// transaction may have applied even when the caller sees a timeout, a
// connection reset or an HTTP 5xx; those outcomes are reported as
// [ErrUnknownOutcome] and must be resolved by querying the transaction, never
// by an unprotected resend. See [Client.ListOrderTransactions].
//
// # Optional idempotency
//
// Payment write requests accept IdempotencyKey. Set a unique key on the first
// attempt, then reuse the key and identical method, path and body within
// 24 hours. Empty keys preserve existing behavior. The SDK validates nonempty
// keys as 1-255 visible ASCII characters without spaces and never retries.
// I0002 permits a later same-key retry; I0003 and C026 require querying first.
// I0001 rejects only the conflicting request; the original may have applied,
// so reconcile it first. A provider V0001 may be saved under the key; a
// corrected request needs a new key. Local [ErrInvalidInput] never sends a
// request and does not consume a key.
// I0003 exposes the query identifier in [Error.ResourceID]. Never switch keys
// to resolve an uncertain charge. After 24 hours the key is treated as new.
//
// Checkout creation has weaker protection: when a resource was created but
// its response was not saved, a resend after 5 minutes may create another.
// Query first; uncertain card binding requires contacting Oen. Query the
// subscription after uncertain cancellation. The /setup3d and /check3d
// endpoints are not covered by this protection.
//
// Card numbers never leave the SDK in full. Every payload the SDK returns is
// sanitized: PANs are reduced to their last four digits and provider secrets
// are replaced with "REDACTED". See [SanitizeJSON].
//
// Webhook payloads are parsed, not authenticated. The Payment API publishes no
// webhook signature, so [Client.ParseWebhook] always reports Verified as false and a
// parsed event is never on its own proof that money moved.
//
// Oen's separate Subscription API is in package
// github.com/howardsun-tw/oen-go/subscription.
package oen
