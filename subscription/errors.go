package subscription

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/howardsun-tw/oen-go/internal/httpx"
)

// Error numbers (errno) from Oen's Subscription API error table.
const (
	// Common errors.
	ErrnoValidation           = "X006"  // 400 field validation failed
	ErrnoPageNotFound         = "X019"  // 400 page does not belong to this list
	ErrnoPageUnreadable       = "X028"  // 400 page cannot be parsed
	ErrnoRecoveryLinkState    = "X016"  // 400 state does not allow a recovery link
	ErrnoInvalidKey           = "X022"  // 401 key missing, malformed or invalid
	ErrnoMissingScope         = "X023"  // 403 key lacks the required scope
	ErrnoRouteNotFound        = "X011"  // 404 path does not exist
	ErrnoSubscriptionNotFound = "SA001" // 404

	// Subscriptions and plans.
	ErrnoCannotCancel           = "SA002" // 409
	ErrnoCannotTerminate        = "SA003" // 409
	ErrnoCannotResume           = "SA004" // 409
	ErrnoCannotChangePlan       = "SA005" // 409
	ErrnoCannotChangePeriod     = "SA006" // 409
	ErrnoStateChanged           = "SA008" // 409 state changed after it was read
	ErrnoUseRecoveryLink        = "SA009" // 409 paused by a failed charge
	ErrnoPaymentDataUnavailable = "SA010" // 503 retry with the same key
	ErrnoPlanNotFound           = "SA011" // 404
	ErrnoSamePlan               = "SA012" // 400
	ErrnoPlanChangedToday       = "SA013" // 409 one plan change per Taipei day
	ErrnoPlanChangeInProgress   = "SA014" // 409
	ErrnoChargeInProgress       = "SA015" // 409 charge pending or undetermined
	ErrnoChangeCompleted        = "SA016" // 409
	ErrnoPeriodEndMalformed     = "SA018" // 400
	ErrnoPeriodEndNotFuture     = "SA019" // 422
	ErrnoProductNotFound        = "SA045" // 404
	ErrnoCustomerNotFound       = "SA046" // 404
	ErrnoAcquirerUnsupported    = "SA047" // 422

	// Refunds.
	ErrnoNotTerminated          = "SA007" // 409
	ErrnoNoRefundableAmount     = "SA021" // 409
	ErrnoRefundExceedsRemaining = "SA022" // 409
	ErrnoTransactionBalance     = "SA023" // 409
	ErrnoTransactionBusy        = "SA024" // 409
	ErrnoRefundInProgress       = "SA025" // 409
	ErrnoRefundKeyFailed        = "SA026" // 409 use a new key to refund again
	ErrnoRefundRejected         = "SA027" // 502 use a new key to refund again
	ErrnoRefundUndetermined     = "SA028" // 503 retry with the same key

	// Idempotency and webhooks.
	ErrnoMissingIdempotencyKey    = "SA034" // 400
	ErrnoIdempotencyKeyExpired    = "SA035" // 409
	ErrnoIdempotencyKeyMismatch   = "SA044" // 409
	ErrnoDeliveryNotFound         = "SA030" // 404
	ErrnoDeliveryInProgress       = "SA031" // 409
	ErrnoWebhookDisabled          = "SA032" // 409
	ErrnoEventUnavailable         = "SA033" // 409
	ErrnoCardReconfirmRequired    = "SA040" // 400
	ErrnoPaymentMethodUnsupported = "SA041" // 400
)

// Kind classifies a failure. Use errors.Is with the sentinels or the exported
// helpers when deciding what to do.
type Kind string

const (
	// KindInvalidRequest means Oen rejected the request itself (HTTP 400 or
	// 422). Nothing was applied; the caller must change the request.
	KindInvalidRequest Kind = "invalid_request"

	// KindUnauthorized means the API key is missing or invalid (HTTP 401).
	KindUnauthorized Kind = "unauthorized"

	// KindForbidden means the key lacks the scope the operation needs
	// (HTTP 403).
	KindForbidden Kind = "forbidden"

	// KindNotFound means the target does not exist or does not belong to the
	// merchant (HTTP 404).
	KindNotFound Kind = "not_found"

	// KindConflict means the target's state does not allow the operation, or
	// the Idempotency-Key conflicts with an earlier request (HTTP 409, and
	// X016). Nothing was applied.
	KindConflict Kind = "conflict"

	// KindInProgress means an earlier charge or refund is still being
	// processed or its result is undetermined (SA015, SA025). It is not a
	// failure. Query later; do not resend.
	KindInProgress Kind = "in_progress"

	// KindDeclined means the payment processor refused a refund (SA027). The
	// money did not move. Check the transaction, and use a new key to try
	// again.
	KindDeclined Kind = "declined"

	// KindRateLimited means an HTTP 429 response arrived. For a write it also
	// matches ErrUnknownOutcome.
	KindRateLimited Kind = "rate_limited"

	// KindUnknownOutcome means the result is unknown: a timeout, a transport
	// failure, an HTTP 5xx, a redirect or an unreadable body. A write may
	// still have applied.
	KindUnknownOutcome Kind = "unknown_outcome"

	// KindInvalidInput is a local validation failure. No request was sent.
	KindInvalidInput Kind = "invalid_input"
)

// Sentinels for errors.Is. Use errors.As with [Error] when the errno, HTTP
// status or retry hint matters.
var (
	ErrInvalidRequest = errors.New("oen subscription: provider rejected the request")
	ErrUnauthorized   = errors.New("oen subscription: provider rejected the API key")
	ErrForbidden      = errors.New("oen subscription: API key lacks the required scope")
	ErrNotFound       = errors.New("oen subscription: resource not found")
	ErrConflict       = errors.New("oen subscription: provider rejected the current state")
	ErrInProgress     = errors.New("oen subscription: an earlier operation is still in progress")
	ErrDeclined       = errors.New("oen subscription: payment processor declined the request")
	ErrRateLimited    = errors.New("oen subscription: provider rate limited the request")
	ErrUnknownOutcome = errors.New("oen subscription: provider outcome is unknown")
	ErrInvalidInput   = errors.New("oen subscription: request is not valid")
)

// Error is every failure that came from Oen or from the transport to it.
type Error struct {
	// Op is the SDK method that failed, such as "CancelSubscription".
	Op string
	// Kind says what the caller may do next.
	Kind Kind
	// Errno is Oen's error number, such as "SA002", when the response had one.
	Errno string
	// Message is Oen's own message text, unmodified.
	Message string
	// HTTPStatus is the response status, or 0 when no response arrived.
	HTTPStatus int
	// RetryAfter is set for [KindRateLimited] when Oen sent a readable
	// Retry-After header.
	RetryAfter time.Duration
	// IdempotencyKey is the key the request carried, empty for operations
	// that take none.
	IdempotencyKey string
	// Err is the underlying cause, such as the transport error.
	Err error
}

func (e *Error) Error() string {
	parts := []string{"oen subscription: " + e.Op, string(e.Kind)}
	if e.Errno != "" {
		parts = append(parts, "errno="+e.Errno)
	}
	if e.HTTPStatus != 0 {
		parts = append(parts, fmt.Sprintf("http=%d", e.HTTPStatus))
	}
	if e.Message != "" {
		parts = append(parts, "message="+e.Message)
	}
	if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	return strings.Join(parts, ": ")
}

func (e *Error) Unwrap() error { return e.Err }

// Is matches the sentinel that corresponds to the error's kind.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrInvalidRequest:
		return e.Kind == KindInvalidRequest
	case ErrUnauthorized:
		return e.Kind == KindUnauthorized
	case ErrForbidden:
		return e.Kind == KindForbidden
	case ErrNotFound:
		return e.Kind == KindNotFound
	case ErrConflict:
		return e.Kind == KindConflict
	case ErrInProgress:
		return e.Kind == KindInProgress
	case ErrDeclined:
		return e.Kind == KindDeclined
	case ErrRateLimited:
		return e.Kind == KindRateLimited
	case ErrUnknownOutcome:
		return e.Kind == KindUnknownOutcome
	default:
		return false
	}
}

// ValidationError is a request the SDK refused to send.
type ValidationError struct {
	Op      string
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	parts := []string{"oen subscription: " + e.Op, string(KindInvalidInput)}
	if e.Field != "" {
		parts = append(parts, "field="+e.Field)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, ": ")
}

func (e *ValidationError) Is(target error) bool { return target == ErrInvalidInput }

// IsUnknownOutcome reports that the result could not be established. A write
// may still have applied.
func IsUnknownOutcome(err error) bool { return errors.Is(err, ErrUnknownOutcome) }

// IsRateLimited reports HTTP 429.
func IsRateLimited(err error) bool { return errors.Is(err, ErrRateLimited) }

// IsNotFound reports HTTP 404.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsConflict reports a state or Idempotency-Key conflict.
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// IsInProgress reports an earlier charge or refund that has not settled.
func IsInProgress(err error) bool { return errors.Is(err, ErrInProgress) }

// RetryAfter returns the delay Oen asked for, or zero when it sent none.
func RetryAfter(err error) time.Duration {
	var target *Error
	if errors.As(err, &target) {
		return target.RetryAfter
	}
	return 0
}

// RetryableWithSameKey reports whether a keyed write may be resent with the
// same Idempotency-Key. Oen documents that a resend with the same key returns
// the original result, and asks for a same-key retry on SA010 and SA028. It
// is false for writes without a key, for SA015 and SA025 (Oen asks the caller
// to query instead of resending), and for definitive refusals. The SDK never
// resends on its own.
func RetryableWithSameKey(err error) bool {
	var target *Error
	if !errors.As(err, &target) || target.IdempotencyKey == "" {
		return false
	}
	return target.Kind == KindUnknownOutcome || target.Kind == KindRateLimited
}

func newValidationError(op, field, message string) *ValidationError {
	return &ValidationError{Op: op, Field: field, Message: message}
}

// classify turns one HTTP response into an error, or nil on success. HTTP
// status decides the kind first; a few error numbers override it where Oen's
// documented meaning cuts against the status.
func classify(op, method string, status int, headers http.Header, body *errorBody, decodeErr error, now time.Time) *Error {
	failure := &Error{Op: op, HTTPStatus: status}
	if body != nil {
		failure.Errno = strings.ToUpper(strings.TrimSpace(body.Errno))
		failure.Message = body.Message
	}

	switch {
	case status == http.StatusTooManyRequests:
		failure.Kind = KindRateLimited
		failure.RetryAfter = httpx.ParseRetryAfter(headers, now)
		failure.Err = decodeErr
		if method != http.MethodGet {
			// A retry hint does not prove that the write did not apply.
			failure.Err = errors.Join(decodeErr, ErrUnknownOutcome)
		}
		return failure
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		if decodeErr != nil {
			failure.Kind = KindUnknownOutcome
			failure.Err = fmt.Errorf("decode response: %w", decodeErr)
			return failure
		}
		return nil
	case status < http.StatusBadRequest:
		failure.Kind = KindUnknownOutcome
		failure.Err = fmt.Errorf("unexpected HTTP status %d", status)
		return failure
	}

	switch failure.Errno {
	case ErrnoRefundRejected:
		failure.Kind = KindDeclined
		return failure
	case ErrnoChargeInProgress, ErrnoRefundInProgress:
		failure.Kind = KindInProgress
		return failure
	}
	if status >= http.StatusInternalServerError {
		failure.Kind = KindUnknownOutcome
		failure.Err = fmt.Errorf("HTTP %d", status)
		return failure
	}
	if failure.Errno == "" {
		failure.Kind = KindUnknownOutcome
		failure.Err = fmt.Errorf("HTTP %d response carries no errno", status)
		return failure
	}
	switch {
	case failure.Errno == ErrnoRecoveryLinkState:
		failure.Kind = KindConflict
	case status == http.StatusUnauthorized:
		failure.Kind = KindUnauthorized
	case status == http.StatusForbidden:
		failure.Kind = KindForbidden
	case status == http.StatusNotFound:
		failure.Kind = KindNotFound
	case status == http.StatusConflict:
		failure.Kind = KindConflict
	case status == http.StatusBadRequest, status == http.StatusUnprocessableEntity:
		failure.Kind = KindInvalidRequest
	default:
		// An undocumented 4xx still says the request was refused, but the
		// SDK cannot tell what the caller should change.
		failure.Kind = KindInvalidRequest
	}
	return failure
}
