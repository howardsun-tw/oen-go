package oen

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/howardsun-tw/oen-go/oentest"
)

// Every endpoint has to fail loudly on a response it cannot read. A zero value
// returned with a nil error is how a caller ends up recording a payment that
// the provider never described.
func TestUnreadableResponsesAreUnknownOutcomesOnEveryEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		data     string
		call     func(*Client) error
	}{
		{
			name:     "checkout without an id",
			endpoint: "/checkout",
			data:     `{"transactionHid":"P1"}`,
			call: func(c *Client) error {
				_, err := c.CreateCheckout(context.Background(), CheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()})
				return err
			},
		},
		{
			name:     "subscription checkout without an id",
			endpoint: "/checkout-subscription",
			data:     `{}`,
			call: func(c *Client) error {
				_, err := c.CreateSubscriptionCheckout(context.Background(), SubscriptionCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()})
				return err
			},
		},
		{
			name:     "schedule checkout with a data field that is not an object",
			endpoint: "/checkout-schedule",
			data:     `"not an object"`,
			call: func(c *Client) error {
				_, err := c.CreateScheduleCheckout(context.Background(), ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()})
				return err
			},
		},
		{
			name:     "token checkout without data",
			endpoint: "/checkout-token",
			data:     `null`,
			call: func(c *Client) error {
				_, err := c.CreateTokenCheckout(context.Background(), TokenCheckoutRequest{CustomID: "b1"})
				return err
			},
		},
		{
			name:     "charge without a transaction id",
			endpoint: "/token/transactions",
			data:     `{"authCode":"831000"}`,
			call: func(c *Client) error {
				_, err := c.ChargeToken(context.Background(), testCharge())
				return err
			},
		},
		{
			name:     "subscribe without a subscription id",
			endpoint: "/token/subscriptions",
			data:     `{"transactionId":"P1","authCode":"831000"}`,
			call: func(c *Client) error {
				_, err := c.SubscribeToken(context.Background(), TokenSubscriptionRequest{
					OrderID: "O1", Token: testToken, Amount: 1000, Items: testItems(),
				})
				return err
			},
		},
		{
			name:     "transaction without an id",
			endpoint: "/transactions/{id}",
			data:     `{"status":"charged","amount":1000}`,
			call: func(c *Client) error {
				_, err := c.GetTransaction(context.Background(), "P1")
				return err
			},
		},
		{
			name:     "transaction whose amount is not a number",
			endpoint: "/transactions/{id}",
			data:     `{"id":"P1","amount":"a thousand"}`,
			call: func(c *Client) error {
				_, err := c.GetTransaction(context.Background(), "P1")
				return err
			},
		},
		{
			name:     "transaction data that is not an object",
			endpoint: "/transactions/{id}",
			data:     `[{"id":"P1"}]`,
			call: func(c *Client) error {
				_, err := c.GetTransaction(context.Background(), "P1")
				return err
			},
		},
		{
			name:     "subscription without an id",
			endpoint: "GET /subscriptions/{subscriptionId}",
			data:     `{"status":"ongoing"}`,
			call: func(c *Client) error {
				_, err := c.GetSubscription(context.Background(), "S1")
				return err
			},
		},
		{
			name:     "cancelled subscription without an id",
			endpoint: "PUT /subscriptions/{subscriptionId}",
			data:     `{"status":"cancelled"}`,
			call: func(c *Client) error {
				_, err := c.CancelSubscription(context.Background(), CancelSubscriptionRequest{SubscriptionID: "S1"})
				return err
			},
		},
		{
			name:     "refunded transaction without an id",
			endpoint: "/refunds/{transactionHid}",
			data:     `{"status":"refunded"}`,
			call: func(c *Client) error {
				_, err := c.Refund(context.Background(), RefundRequest{TransactionHID: "P1", Amount: 1000, Items: testItems()})
				return err
			},
		},
		{
			name:     "transaction list with an unreadable row",
			endpoint: "/order/{orderId}/transactions",
			data:     `{"transactions":[{"status":"charged"}]}`,
			call: func(c *Client) error {
				_, err := c.ListOrderTransactions(context.Background(), "O1")
				return err
			},
		},
		{
			name:     "transaction list that is not a list",
			endpoint: "/transactions",
			data:     `{"rows":[]}`,
			call: func(c *Client) error {
				_, err := c.ListTransactions(context.Background(), ListTransactionsRequest{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, client := newFake(t)
			server.QueueResponse(test.endpoint, oentest.Response{
				Body: []byte(`{"code":"S0000","message":"","data":` + test.data + `}`),
			})
			err := test.call(client)
			hasError(t, err)
			errIs(t, err, ErrUnknownOutcome)
			errIsNot(t, err, ErrDeclined)
			var detail *Error
			isTrue(t, errors.As(err, &detail))
			equal(t, http.StatusOK, detail.HTTPStatus)
			equal(t, CodeSuccess, detail.Code)
		})
	}
}

// Oen wraps a single resource in one more object on some endpoints.
func TestWrappedResourcesAreUnwrapped(t *testing.T) {
	server, client := newFake(t)
	server.QueueResponse("/transactions/{id}", oentest.Response{
		Body: []byte(`{"code":"S0000","data":{"transaction":{"id":"P1","status":"charged","amount":1000}}}`),
	})
	transaction, err := client.GetTransaction(context.Background(), "P1")
	noError(t, err)
	equal(t, "P1", transaction.ID)

	server.QueueResponse("GET /subscriptions/{subscriptionId}", oentest.Response{
		Body: []byte(`{"code":"S0000","data":{"subscription":{"id":"S1","status":"ongoing","amount":1000}}}`),
	})
	subscription, err := client.GetSubscription(context.Background(), "S1")
	noError(t, err)
	equal(t, "S1", subscription.ID)
	equal(t, SubscriptionOngoing, subscription.Status)
}

func TestHostedFlowsNeedAFailureURLToo(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL:           server.URL,
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		DefaultSuccessURL: "https://merchant.example/paid",
	})
	noError(t, err)

	_, err = client.CreateTokenCheckout(context.Background(), TokenCheckoutRequest{CustomID: "b1"})
	hasError(t, err)
	var target *ValidationError
	isTrue(t, asValidationError(err, &target))
	equal(t, "failureUrl", target.Field)
	equal(t, 0, len(server.Requests()))
}

func TestInvalidFinancialAmountsNeverBecomeZero(t *testing.T) {
	for _, field := range []string{"amount", "fee", "refundAmount"} {
		for _, value := range []string{`"bad"`, `0.5`, `9223372036854775808`, `{}`} {
			t.Run(field+value, func(t *testing.T) {
				server, client := newFake(t)
				body := map[string]any{"id": "P1", "amount": 1000}
				body[field] = json.RawMessage(value)
				data, err := json.Marshal(body)
				noError(t, err)
				server.QueueResponse("/transactions/{id}", oentest.Response{Body: append(append([]byte(`{"code":"S0000","data":`), data...), '}')})
				_, err = client.GetTransaction(context.Background(), "P1")
				errIs(t, err, ErrUnknownOutcome)
			})
		}
	}
	for _, value := range []string{`"bad"`, `0.5`, `9223372036854775808`, `null`} {
		t.Run("subscription/"+value, func(t *testing.T) {
			server, client := newFake(t)
			server.QueueResponse("GET /subscriptions/{subscriptionId}", oentest.Response{Body: []byte(`{"code":"S0000","data":{"id":"S1","amount":` + value + `}}`)})
			_, err := client.GetSubscription(context.Background(), "S1")
			errIs(t, err, ErrUnknownOutcome)
		})
	}
}

func TestTransactionScalarCardInfoIsRedacted(t *testing.T) {
	server, client := newFake(t)
	server.QueueResponse("/transactions/{id}", oentest.Response{Body: []byte(`{"code":"S0000","data":{"id":"P1","amount":1000,"paymentMethod":"card","paymentInfo":"4242424242424242"}}`)})
	transaction, err := client.GetTransaction(context.Background(), "P1")
	noError(t, err)
	equal(t, "4242", transaction.PaymentInfo.Text)
	equal(t, "4242", transaction.PaymentInfo.CardLast4)
	notContains(t, string(transaction.RedactedPayload), "4242424242424242")
}
