package subscription

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestProviderErrorsAreClassifiedByStatusAndErrno(t *testing.T) {
	tests := []struct {
		status    int
		errno     string
		kind      Kind
		sentinel  error
		sameKeyOK bool
	}{
		{400, ErrnoValidation, KindInvalidRequest, ErrInvalidRequest, false},
		{400, ErrnoMissingIdempotencyKey, KindInvalidRequest, ErrInvalidRequest, false},
		// X016 is a 400, but it reports the subscription's state.
		{400, ErrnoRecoveryLinkState, KindConflict, ErrConflict, false},
		{401, ErrnoInvalidKey, KindUnauthorized, ErrUnauthorized, false},
		{403, ErrnoMissingScope, KindForbidden, ErrForbidden, false},
		{404, ErrnoSubscriptionNotFound, KindNotFound, ErrNotFound, false},
		{409, ErrnoCannotCancel, KindConflict, ErrConflict, false},
		{409, ErrnoIdempotencyKeyMismatch, KindConflict, ErrConflict, false},
		{409, ErrnoRefundKeyFailed, KindConflict, ErrConflict, false},
		// SA015 and SA025 are not failures: query, do not resend.
		{409, ErrnoChargeInProgress, KindInProgress, ErrInProgress, false},
		{409, ErrnoRefundInProgress, KindInProgress, ErrInProgress, false},
		{422, ErrnoAcquirerUnsupported, KindInvalidRequest, ErrInvalidRequest, false},
		// SA027 is a 502, but it is a definitive refusal.
		{502, ErrnoRefundRejected, KindDeclined, ErrDeclined, false},
		{503, ErrnoRefundUndetermined, KindUnknownOutcome, ErrUnknownOutcome, true},
		{503, ErrnoPaymentDataUnavailable, KindUnknownOutcome, ErrUnknownOutcome, true},
		{500, "", KindUnknownOutcome, ErrUnknownOutcome, true},
		// A 4xx without an errno proves nothing about the request.
		{400, "", KindUnknownOutcome, ErrUnknownOutcome, true},
		{418, "X999", KindInvalidRequest, ErrInvalidRequest, false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.errno), func(t *testing.T) {
			body := `{"message":"no errno"}`
			if tc.errno != "" {
				body = fmt.Sprintf(`{"errno":%q,"message":"oen says"}`, tc.errno)
			}
			_, client := newFake(t, tc.status, body)
			_, err := client.RefundSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k-1"})
			var detail *Error
			isTrue(t, errors.As(err, &detail))
			equal(t, tc.kind, detail.Kind)
			equal(t, tc.status, detail.HTTPStatus)
			equal(t, tc.errno, detail.Errno)
			equal(t, "k-1", detail.IdempotencyKey)
			errIs(t, err, tc.sentinel)
			equal(t, tc.sameKeyOK, RetryableWithSameKey(err))
			if tc.kind != KindUnknownOutcome {
				errIsNot(t, err, ErrUnknownOutcome)
			}
		})
	}
}

func TestErrorTextCarriesErrnoButNotTheKey(t *testing.T) {
	_, client := newFake(t, http.StatusConflict, `{"errno":"SA002","message":"cannot cancel"}`)
	_, err := client.CancelSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
	contains(t, err.Error(), "errno=SA002")
	contains(t, err.Error(), "http=409")
	contains(t, err.Error(), "message=cannot cancel")
	notContains(t, err.Error(), testKey)
	isTrue(t, IsConflict(err))
	isFalse(t, IsNotFound(err))
	isFalse(t, IsInProgress(err))
	isFalse(t, IsRateLimited(err))
}

func TestRateLimitOnReadIsNotUnknownOutcome(t *testing.T) {
	f, client := newFake(t, http.StatusTooManyRequests, `{"errno":"X429"}`)
	_ = f
	_, err := client.GetSubscription(context.Background(), "sub_1")
	isTrue(t, IsRateLimited(err))
	isFalse(t, IsUnknownOutcome(err))
	isFalse(t, RetryableWithSameKey(err))
	equal(t, "X429", errnoOf(err))
}

func TestRetryableWithSameKeyIgnoresOtherErrors(t *testing.T) {
	isFalse(t, RetryableWithSameKey(nil))
	isFalse(t, RetryableWithSameKey(errors.New("plain")))
	isFalse(t, RetryableWithSameKey(&Error{Kind: KindUnknownOutcome}))
	isTrue(t, RetryableWithSameKey(&Error{Kind: KindUnknownOutcome, IdempotencyKey: "k"}))
	isFalse(t, RetryableWithSameKey(&Error{Kind: KindConflict, IdempotencyKey: "k"}))
	equal(t, 0, int(RetryAfter(errors.New("plain"))))
}

func errnoOf(err error) string {
	var detail *Error
	if errors.As(err, &detail) {
		return detail.Errno
	}
	return ""
}
