package subscription

import (
	"net/http"
	"testing"
	"time"
)

// LookupErrno promises the Kind the SDK assigns, so every entry must agree
// with classify for a response at the documented status.
func TestErrnoCatalogMatchesClassification(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	for errno, info := range errnoCatalog {
		t.Run(errno, func(t *testing.T) {
			err := classify("RefundSubscription", http.MethodPost, info.HTTPStatus, nil, &errorBody{Errno: errno}, nil, now)
			isTrue(t, err != nil)
			equal(t, info.Kind, err.Kind)
			isTrue(t, info.Description != "" && info.OfficialDescription != "")
		})
	}
}

func TestLookupErrnoAndDescription(t *testing.T) {
	info, ok := LookupErrno(" sa015 ")
	isTrue(t, ok)
	equal(t, ErrnoChargeInProgress, info.Errno)
	equal(t, http.StatusConflict, info.HTTPStatus)
	equal(t, KindInProgress, info.Kind)

	_, ok = LookupErrno("X999")
	isFalse(t, ok)

	equal(t, "The page token cannot be parsed.", (&Error{Errno: ErrnoPageUnreadable}).Description())
	equal(t, "", (&Error{Errno: "X999"}).Description())
	equal(t, "", (&Error{Kind: KindUnknownOutcome}).Description())
}
