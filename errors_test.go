package oen

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

var classifyNow = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func envelopeFor(code, message string) *envelope {
	return &envelope{Code: rawJSON(code), Message: rawJSON(message)}
}

// The official code table (S0000, A0001, V0001, V0002, T0001..T0005, F0001)
// decides what the caller may do next, so every code has to land in exactly
// one kind. F0001 is a provider-side system error: the request may still have
// applied, so it must never look like a clean refusal.
func TestClassifyMapsOfficialCodes(t *testing.T) {
	tests := []struct {
		code     string
		kind     Kind
		reason   DeclineReason
		declined bool
		unknown  bool
	}{
		{code: CodeUnauthorized, kind: KindUnauthorized},
		{code: CodeInvalidRequest, kind: KindInvalidRequest},
		{code: CodeInvalidTransactionState, kind: KindTransactionState},
		{code: CodeTransactionFailed, kind: KindDeclined, reason: ReasonTransactionFailed, declined: true},
		{code: CodeInvalidCVV, kind: KindDeclined, reason: ReasonInvalidCVV, declined: true},
		{code: CodeCardExpired, kind: KindDeclined, reason: ReasonCardExpired, declined: true},
		{code: CodeInsufficientFunds, kind: KindDeclined, reason: ReasonInsufficientFunds, declined: true},
		{code: CodeCardDeclined, kind: KindDeclined, reason: ReasonCardDeclined, declined: true},
		{code: CodeSystemError, kind: KindUnknownOutcome, unknown: true},
		{code: "T9999", kind: KindDeclined, reason: ReasonUnknown, declined: true},
		{code: "V9999", kind: KindInvalidRequest},
		{code: "A9999", kind: KindUnauthorized},
		// An undocumented prefix cannot be proven harmless, so the caller has
		// to query back instead of assuming the charge never happened.
		{code: "X0001", kind: KindUnknownOutcome, unknown: true},
	}

	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			err := classify("ChargeToken", http.MethodPost, http.StatusOK, nil, envelopeFor(test.code, "provider said so"), nil, classifyNow)
			hasError(t, err)
			equal(t, test.kind, err.Kind)
			equal(t, test.code, err.Code)
			equal(t, "provider said so", err.Message)
			equal(t, "ChargeToken", err.Op)
			equal(t, test.reason, err.DeclineReason())
			equal(t, test.declined, IsDeclined(err))
			equal(t, test.unknown, IsUnknownOutcome(err))
			isFalse(t, IsRateLimited(err))
		})
	}
}

func TestClassifyAcceptsSuccessCode(t *testing.T) {
	equal(t, (*Error)(nil), classify("GetTransaction", http.MethodGet, http.StatusOK, nil, envelopeFor(CodeSuccess, ""), nil, classifyNow))
	// Oen sends the code in upper case; tolerate whitespace and case anyway.
	equal(t, (*Error)(nil), classify("GetTransaction", http.MethodGet, http.StatusOK, nil, envelopeFor(" s0000 ", ""), nil, classifyNow))
}

func TestClassifyMapsTransportAndStatusOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		env      *envelope
		decode   error
		kind     Kind
		contains string
	}{
		{name: "500 is unknown", status: http.StatusInternalServerError, kind: KindUnknownOutcome, contains: "HTTP 500"},
		{name: "502 is unknown", status: http.StatusBadGateway, kind: KindUnknownOutcome, contains: "HTTP 502"},
		{
			name:     "400 without a provider code is unknown",
			status:   http.StatusBadRequest,
			env:      envelopeFor("", "no code here"),
			kind:     KindUnknownOutcome,
			contains: "HTTP 400",
		},
		{
			name:     "400 with a provider code keeps the code",
			status:   http.StatusBadRequest,
			env:      envelopeFor(CodeInvalidRequest, "bad field"),
			kind:     KindInvalidRequest,
			contains: "V0001",
		},
		{
			name:     "401 with the unauthorized code",
			status:   http.StatusUnauthorized,
			env:      envelopeFor(CodeUnauthorized, "bad token"),
			kind:     KindUnauthorized,
			contains: "A0001",
		},
		{
			name:     "unparseable success body is unknown",
			status:   http.StatusOK,
			decode:   errors.New("invalid character 'n'"),
			kind:     KindUnknownOutcome,
			contains: "decode response",
		},
		{
			name:     "success body without a code is unknown",
			status:   http.StatusOK,
			env:      envelopeFor("", ""),
			kind:     KindUnknownOutcome,
			contains: "code is missing",
		},
		{
			name:     "unexpected redirect status is unknown",
			status:   http.StatusFound,
			kind:     KindUnknownOutcome,
			contains: "unexpected HTTP status 302",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := classify("ChargeToken", http.MethodPost, test.status, nil, test.env, test.decode, classifyNow)
			hasError(t, err)
			equal(t, test.kind, err.Kind)
			equal(t, test.status, err.HTTPStatus)
			contains(t, err.Error(), test.contains)
		})
	}
}

func TestClassifyReadsRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "seconds", header: "3", want: 3 * time.Second},
		{name: "http date", header: classifyNow.Add(90 * time.Second).UTC().Format(http.TimeFormat), want: 90 * time.Second},
		{name: "past date", header: classifyNow.Add(-time.Minute).UTC().Format(http.TimeFormat)},
		{name: "negative seconds", header: "-5"},
		{name: "fractional seconds are not HTTP delay-seconds", header: "1.5"},
		{name: "duration units are not HTTP delay-seconds", header: "1m"},
		{name: "signed seconds are invalid", header: "+3"},
		{name: "zero seconds", header: "0"},
		{name: "leading zeroes", header: "003", want: 3 * time.Second},
		{name: "duration overflow saturates", header: "9223372037", want: time.Duration(1<<63 - 1)},
		{name: "integer overflow saturates", header: "99999999999999999999999999", want: time.Duration(1<<63 - 1)},
		{name: "nonsense", header: "soon"},
		{name: "absent"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := http.Header{}
			if test.header != "" {
				headers.Set("Retry-After", test.header)
			}
			err := classify("ChargeToken", http.MethodPost, http.StatusTooManyRequests, headers, nil, nil, classifyNow)
			hasError(t, err)
			equal(t, KindRateLimited, err.Kind)
			equal(t, test.want, err.RetryAfter)
			equal(t, test.want, RetryAfter(err))
			isTrue(t, IsRateLimited(err))
			isTrue(t, IsUnknownOutcome(err))
			isFalse(t, IsDeclined(err))
		})
	}
}

func TestErrorMatchesSentinelsAndUnwraps(t *testing.T) {
	cause := errors.New("dial tcp: connection reset")
	err := &Error{Op: "ChargeToken", Kind: KindUnknownOutcome, Err: cause}

	errIs(t, err, ErrUnknownOutcome)
	errIs(t, err, cause)
	errIsNot(t, err, ErrDeclined)
	equal(t, cause, errors.Unwrap(err))

	var target *Error
	isTrue(t, errors.As(err, &target))
	equal(t, "ChargeToken", target.Op)

	declined := &Error{Op: "ChargeToken", Kind: KindDeclined, Code: CodeInsufficientFunds}
	errIs(t, declined, ErrDeclined)
	errIsNot(t, declined, ErrUnknownOutcome)

	for kind, sentinel := range map[Kind]error{
		KindDeclined:         ErrDeclined,
		KindInvalidRequest:   ErrInvalidRequest,
		KindUnauthorized:     ErrUnauthorized,
		KindTransactionState: ErrTransactionState,
		KindRateLimited:      ErrRateLimited,
		KindUnknownOutcome:   ErrUnknownOutcome,
	} {
		errIs(t, &Error{Kind: kind}, sentinel)
	}
}

func TestValidationErrorNeverLooksLikeAProviderOutcome(t *testing.T) {
	err := newValidationError("ChargeToken", "amount", "must be positive")
	errIs(t, err, ErrInvalidInput)
	errIsNot(t, err, ErrDeclined)
	errIsNot(t, err, ErrUnknownOutcome)
	isFalse(t, IsUnknownOutcome(err))
	isFalse(t, IsDeclined(err))
	contains(t, err.Error(), "amount")
	contains(t, err.Error(), "must be positive")

	var target *ValidationError
	isTrue(t, errors.As(err, &target))
	equal(t, "amount", target.Field)

	mismatch := errProductAmountMismatch("ChargeToken", 150, 149)
	errIs(t, mismatch, ErrProductAmountMismatch)
	errIs(t, mismatch, ErrInvalidInput)
	contains(t, mismatch.Error(), "150")
	contains(t, mismatch.Error(), "149")
}

func TestUnknownOutcomeCarriesTheOperation(t *testing.T) {
	err := unknownOutcome("ChargeToken", errors.New("read response body"))
	errIs(t, err, ErrUnknownOutcome)
	equal(t, "ChargeToken", err.Op)
	contains(t, err.Error(), "ChargeToken")
	contains(t, err.Error(), "read response body")
}

func TestHelpersIgnoreUnrelatedErrors(t *testing.T) {
	err := errors.New("something else")
	isFalse(t, IsDeclined(err))
	isFalse(t, IsUnknownOutcome(err))
	isFalse(t, IsRateLimited(err))
	isFalse(t, IsUnauthorized(err))
	isFalse(t, IsInvalidRequest(err))
	equal(t, time.Duration(0), RetryAfter(err))
	isFalse(t, IsDeclined(nil))
}
