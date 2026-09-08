package oen

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/howardsun-tw/oen-go/oentest"
)

// This snapshot contains all endpoint response examples from the official
// collection, not responses manufactured by oentest. See its source and hash.
//
//go:embed testdata/oen-api-contract.json
var officialContractJSON []byte

type endpointContract struct {
	Method    string
	Path      string
	Responses []json.RawMessage
}

func TestEveryEndpointReportsRateLimitsWithoutRetrying(t *testing.T) {
	for key, call := range endpointCalls() {
		t.Run(key, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"code":"F0001","message":"throttled"}`))
			}))
			t.Cleanup(server.Close)
			client, err := New(contractConfig(server.URL))
			noError(t, err)
			err = call(context.Background(), client)
			errIs(t, err, ErrRateLimited)
			equal(t, !strings.HasPrefix(key, "GET "), IsUnknownOutcome(err))
			errIsNot(t, err, ErrDeclined)
			equal(t, 3*time.Second, RetryAfter(err))
			var detail *Error
			isTrue(t, errors.As(err, &detail))
			equal(t, http.StatusTooManyRequests, detail.HTTPStatus)
			equal(t, "F0001", detail.Code)
			equal(t, int32(1), requests.Load())
		})
	}
}

func officialEndpoints(t *testing.T) []endpointContract {
	t.Helper()
	var contract struct {
		Endpoints []endpointContract
	}
	noError(t, json.Unmarshal(officialContractJSON, &contract))
	if len(contract.Endpoints) == 0 {
		t.Fatal("official endpoint snapshot is empty")
	}
	return contract.Endpoints
}

// Every documented operation must have an executable public SDK call. The
// same calls are used for the timeout and rate-limit conformance checks.
func endpointCalls() map[string]func(context.Context, *Client) error {
	return map[string]func(context.Context, *Client) error{
		"POST /checkout": func(ctx context.Context, c *Client) error {
			_, err := c.CreateCheckout(ctx, CheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()})
			return err
		},
		"POST /checkout-subscription": func(ctx context.Context, c *Client) error {
			_, err := c.CreateSubscriptionCheckout(ctx, SubscriptionCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()})
			return err
		},
		"POST /checkout-schedule": func(ctx context.Context, c *Client) error {
			_, err := c.CreateScheduleCheckout(ctx, ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()})
			return err
		},
		"POST /checkout-token": func(ctx context.Context, c *Client) error {
			_, err := c.CreateTokenCheckout(ctx, TokenCheckoutRequest{CustomID: "B1"})
			return err
		},
		"POST /token/transactions": func(ctx context.Context, c *Client) error {
			_, err := c.ChargeToken(ctx, testCharge())
			return err
		},
		"POST /token/subscriptions": func(ctx context.Context, c *Client) error {
			_, err := c.SubscribeToken(ctx, TokenSubscriptionRequest{OrderID: "O1", Token: testToken, Amount: 1000, Items: testItems()})
			return err
		},
		"GET /transactions/:id": func(ctx context.Context, c *Client) error {
			_, err := c.GetTransaction(ctx, "P1")
			return err
		},
		"GET /order/:orderId/transactions": func(ctx context.Context, c *Client) error {
			_, err := c.ListOrderTransactions(ctx, "O1")
			return err
		},
		"GET /transactions": func(ctx context.Context, c *Client) error {
			_, err := c.ListTransactions(ctx, ListTransactionsRequest{})
			return err
		},
		"GET /subscriptions/:subscriptionId": func(ctx context.Context, c *Client) error {
			_, err := c.GetSubscription(ctx, "S1")
			return err
		},
		"PUT /subscriptions/:subscriptionId": func(ctx context.Context, c *Client) error {
			_, err := c.CancelSubscription(ctx, CancelSubscriptionRequest{SubscriptionID: "S1"})
			return err
		},
		"POST /refunds/:transactionHid": func(ctx context.Context, c *Client) error {
			_, err := c.Refund(ctx, RefundRequest{TransactionHID: "P1", Amount: 1000, Items: testItems()})
			return err
		},
	}
}

func contractConfig(baseURL string) Config {
	return Config{
		BaseURL:           baseURL,
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	}
}

func TestEveryOfficialEndpointAcceptsItsPublishedResponses(t *testing.T) {
	calls := endpointCalls()
	endpoints := officialEndpoints(t)
	equal(t, len(endpoints), len(calls))
	for _, endpoint := range endpoints {
		key := endpoint.Method + " " + endpoint.Path
		call, ok := calls[key]
		if !ok {
			t.Fatalf("no public SDK call covers official endpoint %s", key)
		}
		if len(endpoint.Responses) == 0 {
			t.Fatalf("no official response examples for %s", key)
		}
		for i, body := range endpoint.Responses {
			t.Run(fmt.Sprintf("%s/example-%d", key, i+1), func(t *testing.T) {
				var requests atomic.Int32
				expectedPath := strings.NewReplacer(":id", "P1", ":orderId", "O1", ":subscriptionId", "S1", ":transactionHid", "P1").Replace(endpoint.Path)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != endpoint.Method || r.URL.Path != expectedPath || r.Header.Get("Authorization") != "Bearer "+oentest.DefaultAuthToken {
						http.Error(w, "unexpected method, path or authentication", http.StatusBadRequest)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write(body)
				}))
				t.Cleanup(server.Close)
				client, err := New(contractConfig(server.URL))
				noError(t, err)
				noError(t, call(context.Background(), client))
				equal(t, int32(1), requests.Load())
			})
		}
	}
}

func TestEveryEndpointHonorsTimeoutsBeforeAndAfterHeaders(t *testing.T) {
	for key, call := range endpointCalls() {
		for _, phase := range []string{"headers", "body"} {
			for _, source := range []string{"config", "context", "http-client"} {
				t.Run(key+"/"+phase+"/"+source, func(t *testing.T) {
					var requests atomic.Int32
					stop := make(chan struct{})
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, _ = io.Copy(io.Discard, r.Body)
						requests.Add(1)
						if phase == "body" {
							w.WriteHeader(http.StatusOK)
							w.(http.Flusher).Flush()
						}
						select {
						case <-r.Context().Done():
						case <-stop:
						}
					}))
					t.Cleanup(func() {
						close(stop)
						server.Close()
					})
					cfg := contractConfig(server.URL)
					cfg.Timeout = 2 * time.Second
					parentTimeout := 2 * time.Second
					switch source {
					case "config":
						cfg.Timeout = 100 * time.Millisecond
					case "context":
						parentTimeout = 100 * time.Millisecond
					case "http-client":
						cfg.HTTPClient = &http.Client{Timeout: 100 * time.Millisecond}
					}
					client, err := New(cfg)
					noError(t, err)
					ctx, cancel := context.WithTimeout(context.Background(), parentTimeout)
					defer cancel()
					started := time.Now()
					err = call(ctx, client)
					elapsed := time.Since(started)
					errIs(t, err, ErrUnknownOutcome)
					errIs(t, err, context.DeadlineExceeded)
					if elapsed >= time.Second {
						t.Fatalf("shortest timeout was ignored: %s", elapsed)
					}
					var detail *Error
					isTrue(t, errors.As(err, &detail))
					if phase == "body" {
						equal(t, http.StatusOK, detail.HTTPStatus)
					}
					equal(t, int32(1), requests.Load())
				})
			}
		}
	}
}

func TestEveryEndpointHonorsCancelledContextWithoutSending(t *testing.T) {
	for key, call := range endpointCalls() {
		t.Run(key, func(t *testing.T) {
			server := oentest.New()
			t.Cleanup(server.Close)
			client, err := New(contractConfig(server.URL))
			noError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err = call(ctx, client)
			errIs(t, err, context.Canceled)
			equal(t, 0, len(server.Requests()))
		})
	}
}

func TestRateLimitHeadersSurviveUnreadableBodiesOnEveryEndpoint(t *testing.T) {
	for key, call := range endpointCalls() {
		for _, bodyMode := range []string{"empty", "html", "oversized", "timeout"} {
			t.Run(key+"/"+bodyMode, func(t *testing.T) {
				var requests atomic.Int32
				stop := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					requests.Add(1)
					w.Header().Set("Retry-After", "3")
					w.WriteHeader(http.StatusTooManyRequests)
					switch bodyMode {
					case "html":
						_, _ = w.Write([]byte("<html>rate limited</html>"))
					case "oversized":
						_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
					case "timeout":
						w.(http.Flusher).Flush()
						select {
						case <-r.Context().Done():
						case <-stop:
						}
					}
				}))
				t.Cleanup(func() {
					close(stop)
					server.Close()
				})
				cfg := contractConfig(server.URL)
				if bodyMode == "timeout" {
					cfg.Timeout = 100 * time.Millisecond
				}
				client, err := New(cfg)
				noError(t, err)
				err = call(context.Background(), client)
				errIs(t, err, ErrRateLimited)
				equal(t, !strings.HasPrefix(key, "GET "), IsUnknownOutcome(err))
				equal(t, 3*time.Second, RetryAfter(err))
				if bodyMode == "timeout" {
					errIs(t, err, context.DeadlineExceeded)
				}
				var detail *Error
				isTrue(t, errors.As(err, &detail))
				equal(t, http.StatusTooManyRequests, detail.HTTPStatus)
				equal(t, int32(1), requests.Load())
			})
		}
	}
}
