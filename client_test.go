package oen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/howardsun-tw/oen-go/oentest"
)

// Oen answers a request without a usable bearer token with A0001, and the
// caller must be able to tell that apart from a decline: nothing was charged.
func TestRejectedCredentialsAreNotADecline(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL:           server.URL,
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         "the-wrong-token",
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	})
	noError(t, err)

	_, err = client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrUnauthorized)
	errIsNot(t, err, ErrDeclined)
	errIsNot(t, err, ErrUnknownOutcome)
	isTrue(t, IsUnauthorized(err))
}

func TestEveryRequestCarriesTheDocumentedHeaders(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL:           server.URL,
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		UserAgent:         "oen-go/test",
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	})
	noError(t, err)

	_, err = client.ChargeToken(context.Background(), testCharge())
	noError(t, err)
	_, err = client.ListOrderTransactions(context.Background(), "ORDER00001")
	noError(t, err)

	requests := server.Requests()
	equal(t, 2, len(requests))
	for _, request := range requests {
		equal(t, "Bearer "+oentest.DefaultAuthToken, request.Header.Get("Authorization"))
		equal(t, "application/json", request.Header.Get("Accept"))
		equal(t, "oen-go/test", request.Header.Get("User-Agent"))
	}
	// Only the request that carries a body declares a content type.
	equal(t, "application/json", requests[0].Header.Get("Content-Type"))
	equal(t, "", requests[1].Header.Get("Content-Type"))
	equal(t, 0, len(requests[1].Body))
}

// A body large enough to exhaust memory is not a payment answer. Refusing it
// as an unknown outcome is the safe reading.
func TestOversizedResponseIsAnUnknownOutcome(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	oversized, err := json.Marshal(map[string]any{
		"code": "S0000",
		"data": map[string]any{"id": "P1", "note": strings.Repeat("x", maxResponseBytes)},
	})
	noError(t, err)
	server.QueueResponse("/token/transactions", oentest.Response{Body: oversized})

	client := newTestClient(t, server)
	_, err = client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	contains(t, err.Error(), "1 MiB")
	var detail *Error
	isTrue(t, errors.As(err, &detail))
	equal(t, http.StatusOK, detail.HTTPStatus)
}

// A host that is not there is a transport failure, and a transport failure on
// a charge proves nothing about whether the charge applied.
func TestTransportFailureIsAnUnknownOutcome(t *testing.T) {
	client, err := New(Config{
		BaseURL:           "http://127.0.0.1:1",
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	})
	noError(t, err)

	_, err = client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	contains(t, err.Error(), "transport failure")
}

func TestClientIsUsableFromManyGoroutines(t *testing.T) {
	server, client := newFake(t)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := client.ListOrderTransactions(context.Background(), "ORDER00001")
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		noError(t, <-done)
	}
	equal(t, 8, server.Count("/order/{orderId}/transactions"))
}

func TestResponsesOutsideTheSuccessRangeAreUnknown(t *testing.T) {
	server, client := newFake(t)
	server.QueueResponse("/token/transactions", oentest.Response{StatusCode: http.StatusFound})
	_, err := client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	contains(t, err.Error(), "unexpected HTTP status 302")
}

// A 2xx envelope with a business failure code is still a business failure.
func TestDeclineArrivingWithHTTP200IsStillADecline(t *testing.T) {
	server, client := newFake(t)
	server.QueueResponse("/token/transactions", oentest.Response{
		StatusCode: http.StatusOK,
		Code:       CodeCardExpired,
		Message:    "卡片過期",
	})
	_, err := client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrDeclined)
}

// Redirects must never replay a charge or forward its token to another URL.
func TestChargeDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, custom := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/custom=%t", status, custom), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.URL.Path == "/token/transactions" {
						w.Header().Set("Location", "/replayed-charge")
						w.WriteHeader(status)
						return
					}
					_, _ = w.Write([]byte(`{"code":"S0000","data":{"transactionHid":"P1","authCode":"A1"}}`))
				}))
				defer server.Close()
				var redirects atomic.Int32
				supplied := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
					redirects.Add(1)
					return nil
				}}
				cfg := Config{BaseURL: server.URL, MerchantID: "merchant", AuthToken: "secret"}
				if custom {
					cfg.HTTPClient = supplied
				}
				client, err := New(cfg)
				noError(t, err)
				_, err = client.ChargeToken(context.Background(), testCharge())
				errIs(t, err, ErrUnknownOutcome)
				equal(t, int32(1), requests.Load())
				equal(t, int32(0), redirects.Load())
				// Constructing an SDK client must not change the caller's redirect policy.
				noError(t, supplied.CheckRedirect(nil, nil))
				equal(t, int32(1), redirects.Load())
			})
		}
	}
}
