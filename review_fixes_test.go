package oen

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/howardsun-tw/oen-go/oentest"
)

// A scalar paymentInfo is masked whenever the payload does not positively say
// the value is a non-card reference: a missing or differently cased payment
// method, a nested object, or an object keyed by an undocumented name must not
// let a card number through, while a LINE Pay reference stays intact.
func TestScalarPaymentInfoIsMaskedUnlessTheMethodIsKnownToBeNotACard(t *testing.T) {
	const pan = "4242424242424242"
	masked := []string{
		`{"paymentInfo":"4242424242424242"}`,
		`{"paymentMethod":"Card","paymentInfo":"4242424242424242"}`,
		`{"purpose":"TOKEN","paymentInfo":"4242424242424242"}`,
		`{"paymentMethod":"card","transaction":{"paymentInfo":"4242424242424242"}}`,
		`{"paymentInfo":{"number":"4242424242424242"}}`,
		`{"paymentMethod":"card","paymentInfo":["4242424242424242"]}`,
		`{"paymentInfo":4242424242424242}`,
	}
	for _, raw := range masked {
		clean, err := SanitizeJSON([]byte(raw))
		noError(t, err)
		notContains(t, string(clean), pan)
		contains(t, string(clean), `"4242"`)
	}

	// An explicit non-card method or a value that is not a card number is
	// preserved: LINE Pay ids and other references are what the caller needs.
	for _, raw := range []string{
		`{"paymentMethod":"linePay","paymentInfo":"4242424242424242"}`,
		`{"method":"atm","paymentInfo":"4242424242424242"}`,
		`{"paymentInfo":"2024041200112233"}`,
		`{"paymentInfo":"LP-2024-0412"}`,
	} {
		clean, err := SanitizeJSON([]byte(raw))
		noError(t, err)
		var payload map[string]any
		noError(t, json.Unmarshal(clean, &payload))
		var decoded map[string]any
		noError(t, json.Unmarshal([]byte(raw), &decoded))
		equal(t, decoded["paymentInfo"], payload["paymentInfo"], raw)
	}

	transaction, err := decodeTransaction(json.RawMessage(`{"id":"P1","amount":1000,"paymentInfo":"4242424242424242"}`), time.UTC)
	noError(t, err)
	equal(t, "4242", transaction.PaymentInfo.Text)
	equal(t, "4242", transaction.PaymentInfo.CardLast4)
	notContains(t, string(transaction.RedactedPayload), pan)

	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,"paymentInfo":"4242424242424242"}`))
	noError(t, err)
	equal(t, "4242", event.PaymentInfo.Text)
	notContains(t, string(event.RedactedPayload), pan)
}

func TestLooksLikeCardNumberUsesLengthAndLuhn(t *testing.T) {
	isTrue(t, looksLikeCardNumber("4242424242424242"))
	isTrue(t, looksLikeCardNumber(" 4111111111111111 "))
	isTrue(t, looksLikeCardNumber("378282246310005"))
	isFalse(t, looksLikeCardNumber("4242424242424241"))
	isFalse(t, looksLikeCardNumber("2024041200112233"))
	isFalse(t, looksLikeCardNumber("98557016831048"))
	isFalse(t, looksLikeCardNumber("171291104995271916"))
	isFalse(t, looksLikeCardNumber("4242"))
	isFalse(t, looksLikeCardNumber("4242 4242 4242 4242"))
	isFalse(t, looksLikeCardNumber("42424242424242424242"))
}

// A present timestamp that cannot be read is an error on every resource, the
// same as a malformed amount; only an absent one is a zero time.
func TestUnreadableTimestampsRejectResources(t *testing.T) {
	_, client := newFake(t)
	for _, field := range []string{"createdAt", "paidAt", "refundedAt", "payoutAt"} {
		_, err := decodeTransaction(json.RawMessage(`{"id":"P1","amount":1,"`+field+`":"20240412"}`), time.UTC)
		hasError(t, err, field)
	}
	for _, field := range []string{"startedAt", "createdAt", "nextChargeAt", "cancelledAt"} {
		_, err := decodeSubscription(json.RawMessage(`{"id":"S1","amount":1,"`+field+`":"2024-04-12"}`), time.UTC, true)
		hasError(t, err, field)
	}
	for _, field := range []string{"paidAt", "occurredAt", "nextChargeAt"} {
		_, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,"` + field + `":"soon"}`))
		errIs(t, err, ErrInvalidRequest)
	}
	_, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,"paymentInfo":4242}`))
	errIs(t, err, ErrInvalidRequest)

	transaction, err := decodeTransaction(json.RawMessage(`{"id":"P1","amount":1,"paidAt":null,"refundedAt":""}`), time.UTC)
	noError(t, err)
	isTrue(t, transaction.CreatedAt.IsZero())
	isTrue(t, transaction.PaidAt == nil)
	isTrue(t, transaction.RefundedAt == nil)
}

// Provider text is passed through unmodified: a long note is not silently cut.
func TestProviderTextIsNotTruncated(t *testing.T) {
	long := "note"
	for len(long) < 1200 {
		long += " 備註 note"
	}
	transaction, err := decodeTransaction(json.RawMessage(`{"id":"P1","amount":1,"note":`+string(rawJSON(long))+`}`), time.UTC)
	noError(t, err)
	equal(t, long, transaction.Note)

	transaction, err = decodeTransaction(json.RawMessage(`{"id":"P1","amount":1,"reason":`+string(rawJSON(long))+`}`), time.UTC)
	noError(t, err)
	equal(t, long, transaction.Reason)

	subscription, err := decodeSubscription(json.RawMessage(`{"id":"S1","amount":1,"note":`+string(rawJSON(long))+`,"reason":`+string(rawJSON(long))+`}`), time.UTC, true)
	noError(t, err)
	equal(t, long, subscription.Note)
	equal(t, long, subscription.Reason)

	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":false,"message":` + string(rawJSON(long)) + `}`))
	noError(t, err)
	equal(t, long, event.Message)
}

// A charge callback that omits amount must not read as a zero-amount charge.
func TestWebhookDistinguishesOmittedAmountFromZero(t *testing.T) {
	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,"amount":0}`))
	noError(t, err)
	isTrue(t, event.HasAmount)
	equal(t, Amount(0), event.Amount)

	event, err = client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true}`))
	noError(t, err)
	isFalse(t, event.HasAmount)
	isTrue(t, event.ChargeSucceeded())

	event, err = client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"B1","purpose":"token","success":true,"token":"secret"}`))
	noError(t, err)
	isFalse(t, event.HasAmount)
}

// Nothing from the raw decode may survive into the event: the token and the
// card number are read from the sanitized copy or captured explicitly.
func TestWebhookEventReadsOnlyTheSanitizedCopy(t *testing.T) {
	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"B1","purpose":"token","success":true,"token":"secret","paymentInfo":"4242424242424242","message":"ok"}`))
	noError(t, err)
	equal(t, "secret", event.Token)
	equal(t, "4242", event.PaymentInfo.Text)
	notContains(t, string(event.RedactedPayload), "secret")
	notContains(t, string(event.RedactedPayload), "4242424242424242")
}

// An empty list can arrive as a bare array, an empty or null list key, or an
// object with nothing but a page token; a shape the SDK does not know stays
// an error so it cannot masquerade as "nothing exists".
func TestTransactionListsReadEveryEmptyShape(t *testing.T) {
	for _, data := range []string{`[]`, `{"transactions":[]}`, `{"transactions":null}`, `{}`, `{"page":"x"}`, `{"items":null}`} {
		server, client := newFake(t)
		server.QueueResponse("/order/{orderId}/transactions", oentest.Response{
			Body: []byte(`{"code":"S0000","message":"","data":` + data + `}`),
		})
		transactions, err := client.ListOrderTransactions(context.Background(), "ORDER00001")
		noError(t, err)
		equal(t, 0, len(transactions), data)
	}
	for _, data := range []string{`{"rows":[]}`, `{"transactions":"x"}`, `{"transactions":{}}`, `null`, `"x"`} {
		server, client := newFake(t)
		server.QueueResponse("/order/{orderId}/transactions", oentest.Response{
			Body: []byte(`{"code":"S0000","message":"","data":` + data + `}`),
		})
		_, err := client.ListOrderTransactions(context.Background(), "ORDER00001")
		hasError(t, err, data)
		errIs(t, err, ErrUnknownOutcome)
	}
}

// A resource that embeds another object is still the resource: a transaction
// carrying a nested subscription must not be decoded as that subscription.
func TestSingleResourceOnlyUnwrapsItsOwnKind(t *testing.T) {
	server, client := newFake(t)
	server.QueueResponse("/transactions/{id}", oentest.Response{
		Body: []byte(`{"code":"S0000","data":{"id":"P1","status":"charged","amount":1000,"subscription":{"id":"S1","status":"ongoing","amount":500}}}`),
	})
	transaction, err := client.GetTransaction(context.Background(), "P1")
	noError(t, err)
	equal(t, "P1", transaction.ID)
	equal(t, Amount(1000), transaction.Amount)

	server.QueueResponse("/transactions/{id}", oentest.Response{
		Body: []byte(`{"code":"S0000","data":{"subscription":{"id":"S1","status":"ongoing","amount":500}}}`),
	})
	_, err = client.GetTransaction(context.Background(), "P1")
	errIs(t, err, ErrUnknownOutcome)

	server.QueueResponse("GET /subscriptions/{subscriptionId}", oentest.Response{
		Body: []byte(`{"code":"S0000","data":{"transaction":{"id":"P1","status":"charged","amount":1000}}}`),
	})
	_, err = client.GetSubscription(context.Background(), "S1")
	errIs(t, err, ErrUnknownOutcome)
}

// Return URLs are where Oen sends the payer, so a relative or non-HTTP value
// is refused locally, on the request and in the Config defaults alike.
func TestReturnURLsMustBeAbsoluteHTTP(t *testing.T) {
	server, client := newFake(t)
	for _, test := range []struct{ success, failure, field string }{
		{"/paid", "", "successUrl"},
		{"ftp://merchant.example/paid", "", "successUrl"},
		{"", "merchant.example/failed", "failureUrl"},
		{"", "://nope", "failureUrl"},
	} {
		_, err := client.CreateCheckout(context.Background(), CheckoutRequest{
			OrderID: "O1", Amount: 1000, Items: testItems(), SuccessURL: test.success, FailureURL: test.failure,
		})
		hasError(t, err, test)
		var target *ValidationError
		isTrue(t, asValidationError(err, &target), test)
		equal(t, test.field, target.Field)
	}
	_, err := client.CreateTokenCheckout(context.Background(), TokenCheckoutRequest{SuccessURL: " https://merchant.example/bound "})
	noError(t, err)
	body := bodyOf(t, server.LastRequest("/checkout-token"))
	jsonEqual(t, `"https://merchant.example/bound"`, string(body["successUrl"]))
	equal(t, 1, len(server.Requests()))

	for _, test := range []struct {
		mutate func(*Config)
		field  string
	}{
		{func(c *Config) { c.DefaultSuccessURL = "/paid" }, "defaultSuccessURL"},
		{func(c *Config) { c.DefaultFailureURL = "ftp://merchant.example/failed" }, "defaultFailureURL"},
	} {
		cfg := validConfig()
		test.mutate(&cfg)
		_, err := cfg.normalized()
		hasError(t, err)
		var target *ValidationError
		isTrue(t, asValidationError(err, &target))
		equal(t, test.field, target.Field)
	}
	cfg := validConfig()
	cfg.DefaultSuccessURL = " https://merchant.example/paid "
	normalized, err := cfg.normalized()
	noError(t, err)
	equal(t, "https://merchant.example/paid", normalized.DefaultSuccessURL)
}

// A zero payment interval leaves Oen's default: the key is omitted on both
// endpoints that take one.
func TestZeroPaymentIntervalIsOmitted(t *testing.T) {
	server, client := newFake(t)
	_, err := client.SubscribeToken(context.Background(), TokenSubscriptionRequest{
		OrderID: "O1", Token: testToken, Amount: 1000, Items: testItems(),
	})
	noError(t, err)
	_, present := bodyOf(t, server.LastRequest("/token/subscriptions"))["paymentInterval"]
	isFalse(t, present)

	_, err = client.CreateScheduleCheckout(context.Background(), ScheduleCheckoutRequest{
		OrderID: "O1", Amount: 1000, Items: testItems(),
	})
	noError(t, err)
	_, present = bodyOf(t, server.LastRequest("/checkout-schedule"))["paymentInterval"]
	isFalse(t, present)
}

// Integers on a webhook follow the same grammar as amounts: a value Oen may
// serialise as 1.0 or "12" is read on quantity, period and numberOfPeriods
// exactly as it is on unitPrice, and a fraction is refused on both alike.
func TestWebhookIntegersFollowTheAmountGrammar(t *testing.T) {
	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,
		"period":3.0,"numberOfPeriods":"12",
		"productDetails":[{"productionCode":"P0001","quantity":1.0,"unitPrice":1000.0}]}`))
	noError(t, err)
	equal(t, 3, event.Period)
	equal(t, 12, event.NumberOfPeriods)
	equal(t, 1, len(event.Items))
	equal(t, 1, event.Items[0].Quantity)
	equal(t, Amount(1000), event.Items[0].UnitPrice)

	for _, fields := range []string{
		`"productDetails":[{"quantity":1.5,"unitPrice":1000}]`,
		`"productDetails":[{"quantity":"+1","unitPrice":1000}]`,
		`"period":1.5`,
		`"numberOfPeriods":"+12"`,
	} {
		t.Run(fields, func(t *testing.T) {
			_, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,` + fields + `}`))
			errIs(t, err, ErrInvalidRequest)
		})
	}
}

// Every amount-bearing endpoint encodes the shared purchase fields through one
// builder, so each of them must carry the customer, note and customId it was
// given and omit them when it was not. The three hosted pages also share the
// use3d flag.
func TestEveryPurchaseEndpointSharesThePayloadEncoding(t *testing.T) {
	customer := Customer{ID: "OEN00001", Name: "王小明", Email: "test@oen.tw"}
	endpoints := []struct {
		name   string
		path   string
		hosted bool
		call   func(*Client, bool) error
	}{
		{"CreateCheckout", "/checkout", true, func(c *Client, full bool) error {
			req := CheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()}
			if full {
				req.Customer, req.Note, req.CustomID, req.Use3D = customer, "備註", "custom-1", true
			}
			_, err := c.CreateCheckout(context.Background(), req)
			return err
		}},
		{"CreateSubscriptionCheckout", "/checkout-subscription", true, func(c *Client, full bool) error {
			req := SubscriptionCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()}
			if full {
				req.Customer, req.Note, req.CustomID, req.Use3D = customer, "備註", "custom-1", true
			}
			_, err := c.CreateSubscriptionCheckout(context.Background(), req)
			return err
		}},
		{"CreateScheduleCheckout", "/checkout-schedule", true, func(c *Client, full bool) error {
			req := ScheduleCheckoutRequest{OrderID: "O1", Amount: 1000, Items: testItems()}
			if full {
				req.Customer, req.Note, req.CustomID, req.Use3D = customer, "備註", "custom-1", true
			}
			_, err := c.CreateScheduleCheckout(context.Background(), req)
			return err
		}},
		{"ChargeToken", "/token/transactions", false, func(c *Client, full bool) error {
			req := TokenChargeRequest{OrderID: "O1", Token: testToken, Amount: 1000, Items: testItems()}
			if full {
				req.Customer, req.Note = customer, "備註"
			}
			_, err := c.ChargeToken(context.Background(), req)
			return err
		}},
		{"SubscribeToken", "/token/subscriptions", false, func(c *Client, full bool) error {
			req := TokenSubscriptionRequest{OrderID: "O1", Token: testToken, Amount: 1000, Items: testItems()}
			if full {
				req.Customer, req.Note = customer, "備註"
			}
			_, err := c.SubscribeToken(context.Background(), req)
			return err
		}},
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			server, client := newFake(t)
			noError(t, endpoint.call(client, true))
			body := bodyOf(t, server.LastRequest(endpoint.path))
			jsonEqual(t, `"`+oentest.DefaultMerchantID+`"`, string(body["merchantId"]))
			jsonEqual(t, `"O1"`, string(body["orderId"]))
			jsonEqual(t, `1000`, string(body["amount"]))
			jsonEqual(t, `"TWD"`, string(body["currency"]))
			jsonEqual(t, `"OEN00001"`, string(body["userId"]))
			jsonEqual(t, `"王小明"`, string(body["userName"]))
			jsonEqual(t, `"test@oen.tw"`, string(body["userEmail"]))
			jsonEqual(t, `"備註"`, string(body["note"]))
			jsonEqual(t,
				`[{"productionCode":"P0001","description":"monthly plan","quantity":1,"unit":"個","unitPrice":800},`+
					`{"productionCode":"P0002","description":"service fee","quantity":2,"unit":"個","unitPrice":100}]`,
				string(body["productDetails"]))
			if endpoint.hosted {
				jsonEqual(t, `"custom-1"`, string(body["customId"]))
				jsonEqual(t, `true`, string(body["use3d"]))
				jsonEqual(t, `"https://merchant.example/paid"`, string(body["successUrl"]))
				jsonEqual(t, `"https://merchant.example/failed"`, string(body["failureUrl"]))
			}

			noError(t, endpoint.call(client, false))
			body = bodyOf(t, server.LastRequest(endpoint.path))
			for _, field := range []string{"userId", "userName", "userEmail", "note", "customId", "use3d"} {
				_, present := body[field]
				isFalse(t, present, endpoint.name, field)
			}
		})
	}
}
