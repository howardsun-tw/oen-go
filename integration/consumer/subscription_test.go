package compat_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/howardsun-tw/oen-go/subscription"
)

const subscriptionKey = "sub_sk_0123456789abcdef0123456789abcdef"

type subscriptionFake struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
	keys     []string
}

// newSubscriptionFake answers like the Subscription API: {"data": …} with an
// array for list paths and an object otherwise.
func newSubscriptionFake(t *testing.T, respond func(http.ResponseWriter, *http.Request) bool) *subscriptionFake {
	t.Helper()
	fake := &subscriptionFake{}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		fake.mu.Lock()
		fake.requests = append(fake.requests, r.Method+" "+r.URL.Path)
		fake.keys = append(fake.keys, r.Header.Get("Idempotency-Key"))
		fake.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+subscriptionKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"errno":"X022","message":"invalid key"}`)
			return
		}
		if respond != nil && respond(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && isListPath(r.URL.Path) {
			_, _ = io.WriteString(w, `{"data":[{"id":"x_1","status":"active"}],"paging":{"next":null}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"id":"x_1","status":"active"}}`)
	}))
	t.Cleanup(fake.Close)
	return fake
}

func isListPath(path string) bool {
	switch {
	case path == "/v1/products", path == "/v1/customers", path == "/v1/webhook-deliveries":
		return true
	case strings.HasSuffix(path, "/plans"), strings.HasSuffix(path, "/subscriptions"):
		return true
	}
	return false
}

func newSubscriptionClient(t *testing.T, baseURL string, timeout time.Duration) *subscription.Client {
	t.Helper()
	client, err := subscription.New(subscription.Config{APIKey: subscriptionKey, BaseURL: baseURL, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestAllSubscriptionEndpointsFromConsumer(t *testing.T) {
	fake := newSubscriptionFake(t, nil)
	client := newSubscriptionClient(t, fake.URL, 0)
	ctx := context.Background()
	keyed := subscription.KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k-1", Fields: map[string]any{"x": 1}}
	calls := []func() error{
		func() error {
			_, err := client.CreateProduct(ctx, subscription.CreateProductRequest{Name: "Pro", SubscriptionType: subscription.SubscriptionTypeFixedPeriod})
			return err
		},
		func() error { _, err := client.ListProducts(ctx, ""); return err },
		func() error { _, err := client.GetProduct(ctx, "prod_1"); return err },
		func() error {
			_, err := client.UpdateProduct(ctx, subscription.UpdateProductRequest{ProductID: "prod_1", Fields: map[string]any{"name": "Pro+"}})
			return err
		},
		func() error { _, err := client.GetSubscriptionURL(ctx, "prod_1"); return err },
		func() error {
			_, err := client.CreatePlan(ctx, subscription.CreatePlanRequest{
				ProductID: "prod_1", Name: "Monthly", Price: 299,
				BillingPeriod: subscription.BillingPeriod{Unit: subscription.PeriodMonth, Interval: 1},
			})
			return err
		},
		func() error { _, err := client.ListPlans(ctx, "prod_1", ""); return err },
		func() error {
			_, err := client.UpdatePlan(ctx, subscription.UpdatePlanRequest{ProductID: "prod_1", PlanID: "plan_1", Fields: map[string]any{"price": 399}})
			return err
		},
		func() error { _, err := client.ListProductSubscriptions(ctx, "prod_1", ""); return err },
		func() error { _, err := client.GetSubscription(ctx, "sub_1"); return err },
		func() error { _, err := client.ChangePlan(ctx, keyed); return err },
		func() error { _, err := client.ChangePeriod(ctx, keyed); return err },
		func() error { _, err := client.CancelSubscription(ctx, keyed); return err },
		func() error { _, err := client.ResumeSubscription(ctx, keyed); return err },
		func() error { _, err := client.TerminateSubscription(ctx, keyed); return err },
		func() error { _, err := client.RefundSubscription(ctx, keyed); return err },
		func() error { _, err := client.CreateRecoveryLink(ctx, "sub_1"); return err },
		func() error { _, err := client.ListCustomers(ctx, ""); return err },
		func() error { _, err := client.GetCustomer(ctx, "cus_1"); return err },
		func() error {
			_, err := client.UpdateCustomer(ctx, subscription.UpdateCustomerRequest{CustomerID: "cus_1", Fields: map[string]any{"name": "王小明"}})
			return err
		},
		func() error { _, err := client.ListWebhookDeliveries(ctx, ""); return err },
		func() error { _, err := client.GetWebhookDelivery(ctx, "del_1"); return err },
		func() error { _, err := client.ResendWebhookDelivery(ctx, "del_1", "k-1"); return err },
	}
	for i, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := len(fake.requests); got != 23 {
		t.Fatalf("expected one request per endpoint, got %d", got)
	}
	keyedCount := 0
	for _, key := range fake.keys {
		if key == "k-1" {
			keyedCount++
		}
	}
	if keyedCount != 7 {
		t.Fatalf("expected 7 requests with an Idempotency-Key, got %d", keyedCount)
	}
}

func TestSubscriptionTimeoutAndRateLimitFromConsumer(t *testing.T) {
	for _, scenario := range []string{"timeout", "rate-limit"} {
		t.Run(scenario, func(t *testing.T) {
			release := make(chan struct{})
			fake := newSubscriptionFake(t, func(w http.ResponseWriter, r *http.Request) bool {
				if scenario == "timeout" {
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return true
				}
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(http.StatusTooManyRequests)
				return true
			})
			defer close(release)
			client := newSubscriptionClient(t, fake.URL, 100*time.Millisecond)
			_, err := client.RefundSubscription(context.Background(), subscription.KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k-1"})
			if !subscription.IsUnknownOutcome(err) || !subscription.RetryableWithSameKey(err) {
				t.Fatalf("expected an uncertain refund that may be resent with its key, got %v", err)
			}
			if scenario == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout cause was lost: %v", err)
			}
			if scenario == "rate-limit" && (!subscription.IsRateLimited(err) || subscription.RetryAfter(err) != 3*time.Second) {
				t.Fatalf("rate-limit hint was lost: %v", err)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if got := len(fake.requests); got != 1 {
				t.Fatalf("expected one attempt, got %d", got)
			}
		})
	}
}

func TestSubscriptionWebhookFromConsumer(t *testing.T) {
	const secret = "sub_whsec_consumer"
	body := []byte(`{"id":"evt_1","type":"subscription_renewed","created":1790000000,` +
		`"data":{"subscription":{"id":"sub_1","status":"active"},"paymentDetail":{"amount":299,"currency":"twd"}}}`)
	now := time.Unix(1790000000, 0)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", now.Unix(), body)
	header := http.Header{}
	header.Set(subscription.HeaderSignature, fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil))))

	verifier := subscription.WebhookVerifier{Secret: secret, Now: func() time.Time { return now }}
	event, err := verifier.Verify(body, header)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != subscription.EventSubscriptionRenewed || event.PaymentDetail.Amount != 299 {
		t.Fatalf("unexpected event %+v", event)
	}
	if _, err := verifier.Verify(append(body, ' '), header); !errors.Is(err, subscription.ErrInvalidSignature) {
		t.Fatalf("tampered body was accepted: %v", err)
	}
}
