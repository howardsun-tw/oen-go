package oen

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPaymentIdempotencyHeaderAndValidation(t *testing.T) {
	for _, key := range []string{"", "order-A001-charge-1", strings.Repeat("x", 255), strings.Repeat("x", 256), "has space", "tab\t", "line\r\n", "中文", "\x7f", "\x00"} {
		for endpoint, call := range endpointCallsWithKey(key) {
			t.Run(endpoint+"/"+key, func(t *testing.T) {
				write := !strings.HasPrefix(endpoint, "GET ")
				valid := key == "" || key == "order-A001-charge-1" || key == strings.Repeat("x", 255)
				var count atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
					want := ""
					if write {
						want = key
					}
					equal(t, want, r.Header.Get("Idempotency-Key"))
					body, err := io.ReadAll(r.Body)
					noError(t, err)
					isFalse(t, strings.Contains(string(body), "IdempotencyKey"))
					isFalse(t, strings.Contains(string(body), "idempotencyKey"))
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = io.WriteString(w, `{"code":"A0001"}`)
				}))
				defer server.Close()
				client, err := New(contractConfig(server.URL))
				noError(t, err)
				err = call(context.Background(), client)
				if write && !valid {
					errIs(t, err, ErrInvalidInput)
					equal(t, int32(0), count.Load())
				} else {
					errIs(t, err, ErrUnauthorized)
					equal(t, int32(1), count.Load())
				}
			})
		}
	}
}

func TestPaymentIdempotencyUnknownOutcomeRetainsResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"I0003","message":"pending","detail":{"resourceId":"P123"}}`)
	}))
	defer server.Close()
	client, err := New(contractConfig(server.URL))
	noError(t, err)
	req := testCharge()
	req.IdempotencyKey = "charge-1"
	_, err = client.ChargeToken(context.Background(), req)
	errIs(t, err, ErrUnknownOutcome)
	var detail *Error
	isTrue(t, errors.As(err, &detail))
	equal(t, "P123", detail.ResourceID)
	equal(t, "I0003", detail.Code)
	equal(t, http.StatusConflict, detail.HTTPStatus)
}

func TestPaymentKeyedWriteIsNotReplayedByTheTransport(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"code":"S0000","data":[]}`)
			return
		}
		posts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = buffered.Flush()
		_ = conn.Close()
	}))
	defer server.Close()
	client, err := New(contractConfig(server.URL))
	noError(t, err)
	_, err = client.ListOrderTransactions(context.Background(), "ORDER00001")
	noError(t, err)
	req := testCharge()
	req.IdempotencyKey = "charge-1"
	_, err = client.ChargeToken(context.Background(), req)
	errIs(t, err, ErrUnknownOutcome)
	equal(t, int32(1), posts.Load())
}
