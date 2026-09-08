package oen

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/howardsun-tw/oen-go/oentest"
)

func newTestClient(t *testing.T, server *oentest.Server) *Client {
	t.Helper()
	client, err := New(Config{
		BaseURL:           server.URL,
		CheckoutBaseURL:   "https://oentech.testing.oen.tw",
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		Timeout:           2 * time.Second,
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	})
	noError(t, err)
	return client
}

func newFake(t *testing.T) (*oentest.Server, *Client) {
	t.Helper()
	server := oentest.New()
	t.Cleanup(server.Close)
	return server, newTestClient(t, server)
}

func testItems() []LineItem {
	return []LineItem{
		{ProductionCode: "P0001", Description: "monthly plan", Quantity: 1, Unit: "個", UnitPrice: 800},
		{ProductionCode: "P0002", Description: "service fee", Quantity: 2, Unit: "個", UnitPrice: 100},
	}
}

func bodyOf(t *testing.T, request oentest.Request) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	noError(t, json.Unmarshal(request.Body, &body))
	return body
}

// The four hosted flows differ only in the path they post to and the page the
// payer must be sent to. Getting a redirect path wrong sends payers to a 404
// with no other symptom, so each one is pinned here.
func TestHostedCheckoutFlowsUseTheDocumentedPathsAndRedirects(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		redirect string
		call     func(*Client) (*CheckoutSession, error)
	}{
		{
			name:     "one-time payment page",
			path:     "/checkout",
			redirect: "https://oentech.testing.oen.tw/checkout/" + oentest.DefaultCheckoutID,
			call: func(client *Client) (*CheckoutSession, error) {
				return client.CreateCheckout(context.Background(), CheckoutRequest{
					OrderID: "ORDER00001", Amount: 1000, Items: testItems(),
				})
			},
		},
		{
			name:     "recurring payment page",
			path:     "/checkout-subscription",
			redirect: "https://oentech.testing.oen.tw/checkout/subscription/" + oentest.DefaultCheckoutID,
			call: func(client *Client) (*CheckoutSession, error) {
				return client.CreateSubscriptionCheckout(context.Background(), SubscriptionCheckoutRequest{
					OrderID: "ORDER00001", Amount: 1000, Items: testItems(), NumberOfPeriods: 12,
				})
			},
		},
		{
			name:     "scheduled recurring payment page",
			path:     "/checkout-schedule",
			redirect: "https://oentech.testing.oen.tw/checkout/schedule/" + oentest.DefaultCheckoutID,
			call: func(client *Client) (*CheckoutSession, error) {
				return client.CreateScheduleCheckout(context.Background(), ScheduleCheckoutRequest{
					OrderID: "ORDER00001", Amount: 1000, Items: testItems(),
					PaymentInterval: 3, StartDate: "2026/10/01",
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, client := newFake(t)
			session, err := test.call(client)
			noError(t, err)
			equal(t, oentest.DefaultCheckoutID, session.ID)
			equal(t, oentest.DefaultTransactionHID, session.TransactionHID)
			equal(t, test.redirect, session.RedirectURL)
			equal(t, 1, server.Count(test.path))

			request := server.LastRequest(test.path)
			equal(t, http.MethodPost, request.Method)
			equal(t, "Bearer "+oentest.DefaultAuthToken, request.Header.Get("Authorization"))
			equal(t, "application/json", request.Header.Get("Content-Type"))
			body := bodyOf(t, request)
			jsonEqual(t, `"`+oentest.DefaultMerchantID+`"`, string(body["merchantId"]))
			jsonEqual(t, `"ORDER00001"`, string(body["orderId"]))
			jsonEqual(t, `1000`, string(body["amount"]))
			jsonEqual(t, `"TWD"`, string(body["currency"]))
			jsonEqual(t, `"https://merchant.example/paid"`, string(body["successUrl"]))
			jsonEqual(t, `"https://merchant.example/failed"`, string(body["failureUrl"]))
			jsonEqual(t,
				`[{"productionCode":"P0001","description":"monthly plan","quantity":1,"unit":"個","unitPrice":800},`+
					`{"productionCode":"P0002","description":"service fee","quantity":2,"unit":"個","unitPrice":100}]`,
				string(body["productDetails"]))
		})
	}
}

func TestCreateTokenCheckoutSendsOnlyTheDocumentedFields(t *testing.T) {
	server, client := newFake(t)
	session, err := client.CreateTokenCheckout(context.Background(), TokenCheckoutRequest{
		CustomID:   "binding-1",
		Note:       "save this card",
		SuccessURL: "https://merchant.example/bound",
		FailureURL: "https://merchant.example/not-bound",
	})
	noError(t, err)
	equal(t, oentest.DefaultCheckoutID, session.ID)
	equal(t, "https://oentech.testing.oen.tw/checkout/subscription/create/"+oentest.DefaultCheckoutID, session.RedirectURL)

	body := bodyOf(t, server.LastRequest("/checkout-token"))
	jsonEqual(t, `"`+oentest.DefaultMerchantID+`"`, string(body["merchantId"]))
	jsonEqual(t, `"https://merchant.example/bound"`, string(body["successUrl"]))
	jsonEqual(t, `"binding-1"`, string(body["customId"]))
	jsonEqual(t, `"save this card"`, string(body["note"]))
	// A card-binding page has no amount and no items to bind money to.
	_, hasAmount := body["amount"]
	isFalse(t, hasAmount)
	_, hasItems := body["productDetails"]
	isFalse(t, hasItems)
	_, hasToken := body["token"]
	isFalse(t, hasToken)
}

func TestCheckoutOmitsOptionalFieldsItWasNotGiven(t *testing.T) {
	server, client := newFake(t)
	_, err := client.CreateCheckout(context.Background(), CheckoutRequest{
		OrderID: "ORDER00001", Amount: 1000, Items: testItems(),
	})
	noError(t, err)
	body := bodyOf(t, server.LastRequest("/checkout"))
	for _, field := range []string{"userName", "userEmail", "userId", "note", "customId", "allowedPaymentMethods", "expectedPayoutDate"} {
		_, present := body[field]
		isFalse(t, present, field)
	}
	// use3d defaults to false at Oen, so sending it is only noise.
	_, present := body["use3d"]
	isFalse(t, present)
}

func TestCheckoutSendsEveryOptionalFieldItWasGiven(t *testing.T) {
	server, client := newFake(t)
	_, err := client.CreateCheckout(context.Background(), CheckoutRequest{
		OrderID:               "ORDER00001",
		Amount:                1000,
		Items:                 testItems(),
		Customer:              Customer{ID: "OEN00001", Name: "王小明", Email: "test@oen.tw"},
		AllowedPaymentMethods: []PaymentMethod{MethodCVS, MethodATM},
		Use3D:                 true,
		CustomID:              "custom-1",
		Note:                  "備註",
		ExpectedPayoutDate:    "2026/10/01",
	})
	noError(t, err)
	body := bodyOf(t, server.LastRequest("/checkout"))
	jsonEqual(t, `"OEN00001"`, string(body["userId"]))
	jsonEqual(t, `"王小明"`, string(body["userName"]))
	jsonEqual(t, `"test@oen.tw"`, string(body["userEmail"]))
	jsonEqual(t, `["cvs","atm"]`, string(body["allowedPaymentMethods"]))
	jsonEqual(t, `true`, string(body["use3d"]))
	jsonEqual(t, `"custom-1"`, string(body["customId"]))
	jsonEqual(t, `"備註"`, string(body["note"]))
	jsonEqual(t, `"2026/10/01"`, string(body["expectedPayoutDate"]))
}

func TestScheduleCheckoutSendsItsOwnScheduleFields(t *testing.T) {
	server, client := newFake(t)
	_, err := client.CreateScheduleCheckout(context.Background(), ScheduleCheckoutRequest{
		OrderID: "ORDER00001", Amount: 1000, Items: testItems(),
		NumberOfPeriods: 6, PaymentInterval: 3, StartDate: "2026/10/01",
	})
	noError(t, err)
	body := bodyOf(t, server.LastRequest("/checkout-schedule"))
	jsonEqual(t, `6`, string(body["numberOfPeriods"]))
	jsonEqual(t, `3`, string(body["paymentInterval"]))
	jsonEqual(t, `"2026/10/01"`, string(body["startDate"]))
}

// Every local refusal must happen before the request leaves, so a rejected
// request can never be the one that took the money.
func TestCheckoutValidationHappensBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		name    string
		request CheckoutRequest
		field   string
		is      error
	}{
		{name: "missing order id", request: CheckoutRequest{Amount: 1000, Items: testItems()}, field: "orderId"},
		{name: "zero amount", request: CheckoutRequest{OrderID: "O1", Items: testItems()}, field: "amount"},
		{name: "negative amount", request: CheckoutRequest{OrderID: "O1", Amount: -5, Items: testItems()}, field: "amount"},
		{name: "no items", request: CheckoutRequest{OrderID: "O1", Amount: 1000}, field: "productDetails"},
		{
			name:    "items do not add up",
			request: CheckoutRequest{OrderID: "O1", Amount: 999, Items: testItems()},
			field:   "productDetails",
			is:      ErrProductAmountMismatch,
		},
		{
			name: "quantity is not positive",
			request: CheckoutRequest{OrderID: "O1", Amount: 1000, Items: []LineItem{
				{Description: "x", Quantity: 0, UnitPrice: 1000},
			}},
			field: "productDetails",
		},
		{
			name: "negative unit price",
			request: CheckoutRequest{OrderID: "O1", Amount: 1000, Items: []LineItem{
				{Description: "x", Quantity: 1, UnitPrice: -1000},
			}},
			field: "productDetails",
		},
		{
			name: "line total overflows",
			request: CheckoutRequest{OrderID: "O1", Amount: 1000, Items: []LineItem{
				{Description: "x", Quantity: 4, UnitPrice: Amount(1<<62 + 1)},
			}},
			field: "productDetails",
		},
		{
			name:    "unsupported currency",
			request: CheckoutRequest{OrderID: "O1", Amount: 1000, Currency: "USD", Items: testItems()},
			field:   "currency",
		},
		{
			name:    "bad payout date",
			request: CheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems(), ExpectedPayoutDate: "2026-10-01"},
			field:   "expectedPayoutDate",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, client := newFake(t)
			_, err := client.CreateCheckout(context.Background(), test.request)
			hasError(t, err)
			errIs(t, err, ErrInvalidInput)
			if test.is != nil {
				errIs(t, err, test.is)
			}
			var target *ValidationError
			isTrue(t, asValidationError(err, &target))
			equal(t, test.field, target.Field)
			equal(t, 0, len(server.Requests()))
		})
	}
}

func TestScheduleCheckoutValidatesItsScheduleFields(t *testing.T) {
	for _, test := range []struct {
		name    string
		request ScheduleCheckoutRequest
		field   string
	}{
		{
			name:    "interval below range",
			request: ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems(), PaymentInterval: -1},
			field:   "paymentInterval",
		},
		{
			name:    "interval above range",
			request: ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems(), PaymentInterval: 13},
			field:   "paymentInterval",
		},
		{
			name:    "start date in the wrong format",
			request: ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems(), StartDate: "01/10/2026"},
			field:   "startDate",
		},
		{
			name:    "negative number of periods",
			request: ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems(), NumberOfPeriods: -1},
			field:   "numberOfPeriods",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, client := newFake(t)
			_, err := client.CreateScheduleCheckout(context.Background(), test.request)
			hasError(t, err)
			var target *ValidationError
			isTrue(t, asValidationError(err, &target))
			equal(t, test.field, target.Field)
			equal(t, 0, len(server.Requests()))
		})
	}
}

func TestHostedFlowsNeedReturnURLs(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL:    server.URL,
		MerchantID: oentest.DefaultMerchantID,
		AuthToken:  oentest.DefaultAuthToken,
	})
	noError(t, err)

	_, err = client.CreateCheckout(context.Background(), CheckoutRequest{
		OrderID: "O1", Amount: 1000, Items: testItems(),
	})
	hasError(t, err)
	var target *ValidationError
	isTrue(t, asValidationError(err, &target))
	equal(t, "successUrl", target.Field)
	equal(t, 0, len(server.Requests()))
}

// Without a checkout host the SDK cannot know where to send the payer, and
// inventing one would send them to a host that is not the merchant's.
func TestSessionWithoutCheckoutHostHasNoRedirectURL(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	client, err := New(Config{
		BaseURL:           server.URL,
		MerchantID:        oentest.DefaultMerchantID,
		AuthToken:         oentest.DefaultAuthToken,
		DefaultSuccessURL: "https://merchant.example/paid",
		DefaultFailureURL: "https://merchant.example/failed",
	})
	noError(t, err)
	session, err := client.CreateCheckout(context.Background(), CheckoutRequest{
		OrderID: "O1", Amount: 1000, Items: testItems(),
	})
	noError(t, err)
	equal(t, oentest.DefaultCheckoutID, session.ID)
	equal(t, "", session.RedirectURL)
}

func TestCheckoutReportsAResponseWithoutAnIdentifier(t *testing.T) {
	server, client := newFake(t)
	server.Success("/checkout", map[string]any{"transactionHid": "P1"})
	_, err := client.CreateCheckout(context.Background(), CheckoutRequest{
		OrderID: "O1", Amount: 1000, Items: testItems(),
	})
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
}
