package oen

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/howardsun-tw/oen-go/internal/httpx"
)

// Response codes from Oen's official error table
// (https://developer.oen.tw/api/error-codes/, checked 2026-10-30). Any other
// code is treated by its first letter, and an unrecognised letter is treated
// as an unknown outcome rather than as a refusal. [LookupCode] describes each
// one.
const (
	CodeIdempotencyConflict       = "I0001" // same key with a different request
	CodeIdempotencyInProgress     = "I0002" // retry the same request and key later
	CodeIdempotencyUnknownOutcome = "I0003" // query; do not charge with a new key
	CodePaymentUnknownOutcome     = "C026"  // query; do not charge with a new key
	CodeSuccess                   = "S0000" // success (執行成功)
	CodeUnauthorized              = "A0001" // unauthorized (未授權)
	CodeTokenExpired              = "A0002" // token expired (token 已過期)
	CodeInvalidRequest            = "V0001" // invalid request (請求錯誤)
	CodeInvalidTransactionState   = "V0002" // invalid transaction state (交易狀態錯誤)
	CodeAmountOverLimit           = "V0003" // amount over the single-payment limit
	CodeMissingPaymentMethods     = "V0004" // strict payment methods without a list
	CodePaymentMethodInactive     = "V0005" // a listed payment method is not activated
	CodeNoPaymentMethodForAmount  = "V0006" // no listed payment method accepts the amount
	CodeCurrencyNotSupported      = "V0007" // strict payment methods need TWD
	CodeTransactionFailed         = "T0001" // transaction failed (交易失敗)
	CodeInvalidCVV                = "T0002" // invalid CVV (安全碼 CVV 錯誤)
	CodeCardExpired               = "T0003" // card expired (卡片過期)
	CodeInsufficientFunds         = "T0004" // insufficient funds (額度不足)
	CodeCardDeclined              = "T0005" // authorization declined (拒絕授權)
	CodeNon3DChargeUnavailable    = "T0009" // domain cannot charge without 3-D Secure
	CodeTooManyDeclines           = "T0010" // token declined too often; nothing was sent
	CodeRefundFailed              = "R0001" // refund failed; query before acting
	CodeRiskRejected              = "K0001" // risk control refused the payment
	CodeSystemError               = "F0001" // system error (系統錯誤)
)

// Kind classifies the response. Use errors.Is or the exported helpers when
// deciding what to do: a rate-limited write is also an unknown outcome.
type Kind string

const (
	// KindDeclined is a definitive refusal. The money did not move and
	// resending the identical request will be refused again.
	KindDeclined Kind = "declined"

	// KindInvalidRequest means Oen rejected this request. For I0001, the
	// original request with the same key may already have applied; reconcile
	// it before deciding on a new operation. A provider V0001 may be saved
	// under the key, so a corrected request needs a new key.
	KindInvalidRequest Kind = "invalid_request"

	// KindUnauthorized means the auth token or merchant is not accepted.
	// Nothing was applied.
	KindUnauthorized Kind = "unauthorized"

	// KindTransactionState means the target transaction or subscription is not
	// in a state that allows the operation, such as refunding a refund.
	// Nothing was applied.
	KindTransactionState Kind = "transaction_state"

	// KindRateLimited means an HTTP 429 response arrived. For a state-changing
	// request it also matches ErrUnknownOutcome: a retry hint does not prove
	// that no payment or other side effect occurred.
	KindRateLimited Kind = "rate_limited"

	// KindUnknownOutcome means the result is genuinely unknown: a timeout, a
	// transport failure, an HTTP 5xx, an unreadable body or a provider system
	// error. A side-effecting request may still have applied. Query the
	// transaction before doing anything else; never resend it blindly.
	KindUnknownOutcome Kind = "unknown_outcome"

	// KindInvalidInput is a local validation failure. No request was sent.
	KindInvalidInput Kind = "invalid_input"

	// KindInvalidSignature means a webhook did not carry a valid
	// OenPay-Signature for its body. Answer with a 4xx status and do not act
	// on the body.
	KindInvalidSignature Kind = "invalid_signature"
)

// Sentinels for errors.Is. They carry no provider detail; use errors.As with
// [Error] when the code, HTTP status or retry hint matters.
var (
	ErrDeclined         = errors.New("oen: provider declined the request")
	ErrInvalidRequest   = errors.New("oen: provider rejected the request")
	ErrUnauthorized     = errors.New("oen: provider rejected the credentials")
	ErrTransactionState = errors.New("oen: provider rejected the transaction state")
	ErrRateLimited      = errors.New("oen: provider rate limited the request")
	ErrUnknownOutcome   = errors.New("oen: provider outcome is unknown")
	ErrInvalidInput     = errors.New("oen: request is not valid")
	ErrInvalidSignature = errors.New("oen: webhook signature is not valid")

	// ErrProductAmountMismatch means the line items do not add up to the
	// amount. Oen refuses that combination, so the SDK refuses it locally
	// instead of spending a provider call on it.
	ErrProductAmountMismatch = errors.New("oen: product details do not add up to the amount")
)

// DeclineReason is the business reason behind a [KindDeclined] error.
type DeclineReason string

const (
	ReasonUnknown           DeclineReason = ""
	ReasonTransactionFailed DeclineReason = "transaction_failed"
	ReasonInvalidCVV        DeclineReason = "invalid_cvv"
	ReasonCardExpired       DeclineReason = "card_expired"
	ReasonInsufficientFunds DeclineReason = "insufficient_funds"
	ReasonCardDeclined      DeclineReason = "card_declined"
)

// Error is every failure that came from Oen or from the transport to it.
type Error struct {
	// Op is the SDK method that failed, such as "ChargeToken".
	Op string
	// Kind says what the caller may do next.
	Kind Kind
	// Code is Oen's response code when the response carried one.
	Code string
	// ResourceID is detail.resourceId on I0003, for transaction or subscription queries.
	ResourceID string
	// Message is Oen's own message text, unmodified.
	Message string
	// HTTPStatus is the response status, or 0 when no response arrived.
	HTTPStatus int
	// RetryAfter is set for [KindRateLimited] when Oen sent a Retry-After
	// header the SDK could read.
	RetryAfter time.Duration
	// Err is the underlying cause, such as the transport error.
	Err error
}

func (e *Error) Error() string {
	parts := make([]string, 0, 5)
	parts = append(parts, "oen: "+e.Op)
	parts = append(parts, string(e.Kind))
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
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

// Is matches the package sentinel that corresponds to the error's kind.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrDeclined:
		return e.Kind == KindDeclined
	case ErrInvalidRequest:
		return e.Kind == KindInvalidRequest
	case ErrUnauthorized:
		return e.Kind == KindUnauthorized
	case ErrTransactionState:
		return e.Kind == KindTransactionState
	case ErrRateLimited:
		return e.Kind == KindRateLimited
	case ErrUnknownOutcome:
		return e.Kind == KindUnknownOutcome
	case ErrInvalidInput:
		return e.Kind == KindInvalidInput
	case ErrInvalidSignature:
		return e.Kind == KindInvalidSignature
	default:
		return false
	}
}

// DeclineReason maps the provider code onto a business reason. It is
// [ReasonUnknown] for anything that is not a documented decline.
func (e *Error) DeclineReason() DeclineReason {
	if e.Kind != KindDeclined {
		return ReasonUnknown
	}
	switch normalizeCode(e.Code) {
	case CodeTransactionFailed:
		return ReasonTransactionFailed
	case CodeInvalidCVV:
		return ReasonInvalidCVV
	case CodeCardExpired:
		return ReasonCardExpired
	case CodeInsufficientFunds:
		return ReasonInsufficientFunds
	case CodeCardDeclined:
		return ReasonCardDeclined
	default:
		return ReasonUnknown
	}
}

// ValidationError is a request the SDK refused to send. No provider state
// changed, so it never satisfies [ErrUnknownOutcome] or [ErrDeclined].
type ValidationError struct {
	Op      string
	Field   string
	Message string
	Err     error
}

func (e *ValidationError) Error() string {
	parts := []string{"oen: " + e.Op, string(KindInvalidInput)}
	if e.Field != "" {
		parts = append(parts, "field="+e.Field)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, ": ")
}

func (e *ValidationError) Unwrap() error { return e.Err }

func (e *ValidationError) Is(target error) bool { return target == ErrInvalidInput }

// IsDeclined reports a definitive provider refusal.
func IsDeclined(err error) bool { return errors.Is(err, ErrDeclined) }

// IsInvalidRequest reports a request Oen refused to process.
func IsInvalidRequest(err error) bool { return errors.Is(err, ErrInvalidRequest) }

// IsUnauthorized reports rejected credentials.
func IsUnauthorized(err error) bool { return errors.Is(err, ErrUnauthorized) }

// IsRateLimited reports HTTP 429. Check IsUnknownOutcome before deciding
// whether to resend a state-changing request after the indicated delay.
func IsRateLimited(err error) bool { return errors.Is(err, ErrRateLimited) }

// IsUnknownOutcome reports that the provider result could not be established.
// A side-effecting request may still have applied; query before retrying.
func IsUnknownOutcome(err error) bool { return errors.Is(err, ErrUnknownOutcome) }

// RetryAfter returns the delay Oen asked for, or zero when it sent none.
func RetryAfter(err error) time.Duration {
	var target *Error
	if errors.As(err, &target) {
		return target.RetryAfter
	}
	return 0
}

func newValidationError(op, field, message string) *ValidationError {
	return &ValidationError{Op: op, Field: field, Message: message}
}

func errProductAmountMismatch(op string, amount, total Amount) *ValidationError {
	return &ValidationError{
		Op:      op,
		Field:   "productDetails",
		Message: fmt.Sprintf("line items total %s but amount is %s", total, amount),
		Err:     ErrProductAmountMismatch,
	}
}

func unknownOutcome(op string, err error) *Error {
	if err == nil {
		err = errors.New("provider response could not be verified")
	}
	return &Error{Op: op, Kind: KindUnknownOutcome, Err: err}
}

// classify turns one HTTP response into an error, or nil when Oen reported
// success. status, headers and env describe the response; decodeErr is set
// when the body was not JSON.
func classify(op, method string, status int, headers http.Header, env *envelope, decodeErr error, now time.Time) *Error {
	if status == http.StatusTooManyRequests {
		cause := decodeErr
		if method != http.MethodGet && method != http.MethodHead {
			cause = errors.Join(cause, ErrUnknownOutcome)
		}
		return &Error{
			Op:         op,
			Kind:       KindRateLimited,
			HTTPStatus: status,
			RetryAfter: httpx.ParseRetryAfter(headers, now),
			Code:       envCode(env),
			Message:    envMessage(env),
			Err:        cause,
		}
	}
	if status >= http.StatusInternalServerError {
		return &Error{
			Op:         op,
			Kind:       KindUnknownOutcome,
			HTTPStatus: status,
			Code:       envCode(env),
			Message:    envMessage(env),
			Err:        fmt.Errorf("HTTP %d", status),
		}
	}
	if status >= http.StatusBadRequest {
		code := envCode(env)
		if decodeErr != nil || env == nil || code == "" {
			return &Error{
				Op:         op,
				Kind:       KindUnknownOutcome,
				HTTPStatus: status,
				Message:    envMessage(env),
				Err:        fmt.Errorf("HTTP %d response carries no provider code", status),
			}
		}
		return providerError(op, status, code, envMessage(env))
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return &Error{
			Op:         op,
			Kind:       KindUnknownOutcome,
			HTTPStatus: status,
			Err:        fmt.Errorf("unexpected HTTP status %d", status),
		}
	}
	if decodeErr != nil {
		return &Error{
			Op:         op,
			Kind:       KindUnknownOutcome,
			HTTPStatus: status,
			Err:        fmt.Errorf("decode response: %w", decodeErr),
		}
	}
	if env == nil {
		return &Error{Op: op, Kind: KindUnknownOutcome, HTTPStatus: status, Err: errors.New("response envelope is missing")}
	}
	code := envCode(env)
	if code == "" {
		return &Error{Op: op, Kind: KindUnknownOutcome, HTTPStatus: status, Err: errors.New("response code is missing")}
	}
	if normalizeCode(code) == CodeSuccess {
		return nil
	}
	return providerError(op, status, code, envMessage(env))
}

// providerError maps one non-success provider code onto a kind. The mapping
// follows the documented table first and the code's letter second, because an
// undocumented code still tells the caller which family it belongs to.
func providerError(op string, status int, code, message string) *Error {
	normalized := normalizeCode(code)
	kind := KindUnknownOutcome
	switch {
	case normalized == CodeIdempotencyConflict:
		kind = KindInvalidRequest
	case normalized == CodeSystemError:
		kind = KindUnknownOutcome
	case normalized == CodeInvalidTransactionState:
		kind = KindTransactionState
	case normalized == CodeRiskRejected:
		// A risk-control refusal is definitive: Oen documents it as not
		// retryable and asks the merchant to contact them.
		kind = KindDeclined
	case strings.HasPrefix(normalized, "T"):
		kind = KindDeclined
	case strings.HasPrefix(normalized, "V"):
		kind = KindInvalidRequest
	case strings.HasPrefix(normalized, "A"):
		kind = KindUnauthorized
	case strings.HasPrefix(normalized, "F"):
		kind = KindUnknownOutcome
	}
	return &Error{Op: op, Kind: kind, Code: code, Message: message, HTTPStatus: status}
}

func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// unknownResponseOutcome retains the response context even when its data
// cannot be decoded. An HTTP response arrived; status zero would hide that.
func unknownResponseOutcome(op string, resp response, cause error) *Error {
	err := unknownOutcome(op, cause)
	err.HTTPStatus = resp.status
	err.Code = envCode(&resp.env)
	err.Message = envMessage(&resp.env)
	return err
}
