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
// by sending the request again. See [Client.ListOrderTransactions].
//
// Card numbers never leave the SDK in full. Every payload the SDK returns is
// sanitized: PANs are reduced to their last four digits and provider secrets
// are replaced with "REDACTED". See [SanitizeJSON].
//
// Webhook payloads are parsed, not authenticated. Oen publishes no webhook
// signature, so [Client.ParseWebhook] always reports Verified as false and a
// parsed event is never on its own proof that money moved.
package oen
