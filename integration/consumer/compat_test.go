package compat_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	oen "github.com/howardsun-tw/oen-go"
	"github.com/howardsun-tw/oen-go/oentest"
)

// The runner sets this consumer module's go directive to the current toolchain,
// exercising its defaults without changing the SDK's minimum Go version.
func TestAllEndpointsFromConsumer(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	client, err := oen.New(oen.Config{
		BaseURL:           server.URL,
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	items := []oen.LineItem{{ProductionCode: "P1", Description: "plan", Quantity: 1, Unit: "item", UnitPrice: 1000}}
	if _, err := client.CreateCheckout(ctx, oen.CheckoutRequest{OrderID: "O1", Amount: 1000, Items: items}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateSubscriptionCheckout(ctx, oen.SubscriptionCheckoutRequest{OrderID: "O1", Amount: 1000, Items: items}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateScheduleCheckout(ctx, oen.ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: items}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateTokenCheckout(ctx, oen.TokenCheckoutRequest{CustomID: "B1"}); err != nil {
		t.Fatal(err)
	}
	charged, err := client.ChargeToken(ctx, oen.TokenChargeRequest{OrderID: "O1", Token: "test-token", Amount: 1000, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	subscribed, err := client.SubscribeToken(ctx, oen.TokenSubscriptionRequest{OrderID: "O2", Token: "test-token", Amount: 1000, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetTransaction(ctx, charged.TransactionHID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetSubscription(ctx, subscribed.SubscriptionID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTransactions(ctx, oen.ListTransactionsRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListOrderTransactions(ctx, "O1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CancelSubscription(ctx, oen.CancelSubscriptionRequest{SubscriptionID: subscribed.SubscriptionID}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Refund(ctx, oen.RefundRequest{TransactionHID: charged.TransactionHID, Amount: 1000, Items: items}); err != nil {
		t.Fatal(err)
	}
	if got := len(server.Requests()); got != 12 {
		t.Fatalf("expected one request per endpoint, got %d", got)
	}
}

func TestTimeoutAndRateLimitFromConsumer(t *testing.T) {
	for _, scenario := range []string{"timeout", "rate-limit"} {
		t.Run(scenario, func(t *testing.T) {
			server := oentest.New()
			t.Cleanup(server.Close)
			if scenario == "timeout" {
				server.Hang("/token/transactions")
			} else {
				server.QueueResponse("/token/transactions", oentest.Response{
					StatusCode: http.StatusTooManyRequests,
					Headers:    http.Header{"Retry-After": []string{"3"}},
				})
			}
			client, err := oen.New(oen.Config{
				BaseURL: server.URL, MerchantID: oentest.DefaultMerchantID,
				AuthToken: oentest.DefaultAuthToken, Timeout: 100 * time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ChargeToken(context.Background(), oen.TokenChargeRequest{
				OrderID: "O1", Token: "test-token", Amount: 1000,
				Items: []oen.LineItem{{ProductionCode: "P1", Description: "plan", Quantity: 1, Unit: "item", UnitPrice: 1000}},
			})
			if !oen.IsUnknownOutcome(err) {
				t.Fatalf("expected uncertain charge, got %v", err)
			}
			if scenario == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout cause was lost: %v", err)
			}
			if scenario == "rate-limit" && (!oen.IsRateLimited(err) || oen.RetryAfter(err) != 3*time.Second) {
				t.Fatalf("rate-limit hint was lost: %v", err)
			}
			if got := len(server.Requests()); got != 1 {
				t.Fatalf("expected one attempt, got %d", got)
			}
		})
	}
}
