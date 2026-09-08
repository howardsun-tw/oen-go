package oen

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The webhook table in Oen's documentation is the contract here; this is a
// full charge callback exactly as documented.
func TestParseWebhookReadsAChargeCallback(t *testing.T) {
	_, client := newFake(t)
	raw := []byte(`{
		"merchantId":"oentech",
		"success":true,
		"id":"2gZlnLRUiOZlnzEFvMrcURtTlHg",
		"purpose":"charge",
		"status":"charged",
		"transactionHid":"P20240517VRTJYBAJ",
		"action":"onetime",
		"amount":1000,
		"currency":"TWD",
		"orderId":"ORDER00001",
		"userId":"OEN00001",
		"customId":"custom-1",
		"paymentMethod":"card",
		"paymentInfo":"4242",
		"authCode":"831000",
		"paidAt":"2024-05-17T09:12:00.000Z"
	}`)

	event, err := client.ParseWebhook(raw)
	noError(t, err)
	equal(t, PurposeCharge, event.Purpose)
	isTrue(t, event.Success)
	isTrue(t, event.HasOutcome)
	isTrue(t, event.ChargeSucceeded())
	isFalse(t, event.TokenBound())
	equal(t, "2gZlnLRUiOZlnzEFvMrcURtTlHg", event.ID)
	equal(t, "P20240517VRTJYBAJ", event.TransactionHID)
	equal(t, StatusCharged, event.Status)
	equal(t, ActionOneTime, event.Action)
	equal(t, Amount(1000), event.Amount)
	equal(t, "TWD", event.Currency)
	equal(t, "ORDER00001", event.OrderID)
	equal(t, "OEN00001", event.Customer.ID)
	equal(t, "custom-1", event.CustomID)
	equal(t, MethodCard, event.PaymentMethod)
	equal(t, "4242", event.PaymentInfo.CardLast4)
	equal(t, "831000", event.AuthCode)
	hasTime(t, event.PaidAt)
	equal(t, "", event.Token)

	// A parsed callback is never proof on its own.
	isFalse(t, event.Verified)
}

func TestParseWebhookReadsATokenCallback(t *testing.T) {
	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{
		"merchantId":"oentech",
		"success":true,
		"id":"2eqe6kF33wbq9Dy1UNoMPzY7tal",
		"purpose":"token",
		"customId":"binding-1",
		"token":"2etM3aQSCMWv7OGYQ6gDWtcOJaR",
		"paymentInfo":"4242"
	}`))
	noError(t, err)
	equal(t, PurposeToken, event.Purpose)
	isTrue(t, event.TokenBound())
	equal(t, "binding-1", event.CustomID)
	equal(t, testToken, event.Token)
	equal(t, "4242", event.PaymentInfo.CardLast4)
	// On a token callback Oen puts the /checkout-token session id in "id".
	equal(t, "2eqe6kF33wbq9Dy1UNoMPzY7tal", event.ID)
	isFalse(t, event.Verified)
	notContains(t, string(event.RedactedPayload), testToken)
}

// A failed binding must not hand back a token, whatever the payload contains.
func TestFailedTokenCallbackNeverYieldsAToken(t *testing.T) {
	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{
		"merchantId":"oentech",
		"success":false,
		"id":"session-1",
		"purpose":"token",
		"customId":"binding-2",
		"token":"2etM3aQSCMWv7OGYQ6gDWtcOJaR"
	}`))
	noError(t, err)
	isFalse(t, event.TokenBound())
	equal(t, "", event.Token)
	notContains(t, string(event.RedactedPayload), testToken)
}

func TestParseWebhookReadsSubscriptionInstalments(t *testing.T) {
	_, client := newFake(t)
	event, err := client.ParseWebhook([]byte(`{
		"merchantId":"oentech",
		"success":true,
		"id":"tx-2",
		"purpose":"charge",
		"status":"charged",
		"action":"subscription",
		"amount":1000,
		"subscriptionId":"S20240412YOARH7GM",
		"period":3,
		"numberOfPeriods":12,
		"nextChargeAt":"2026-10-11T16:00:00.000Z",
		"productDetails":[{"productionCode":"P0001","description":"plan","quantity":1,"unit":"個","unitPrice":1000}]
	}`))
	noError(t, err)
	equal(t, ActionSubscription, event.Action)
	equal(t, "S20240412YOARH7GM", event.SubscriptionID)
	equal(t, 3, event.Period)
	equal(t, 12, event.NumberOfPeriods)
	hasTime(t, event.NextChargeAt)
	equal(t, 1, len(event.Items))
	equal(t, "P0001", event.Items[0].ProductionCode)
	equal(t, Amount(1000), event.Items[0].UnitPrice)
}

// A valid callback always carries a boolean outcome. Status remains available
// independently so callers can distinguish an in-flight charge.
func TestWebhookKeepsStatusSeparateFromOutcome(t *testing.T) {
	_, client := newFake(t)
	for _, status := range []TransactionStatus{StatusInitiated, StatusCharging, StatusCharged, StatusFailed, "future"} {
		raw := []byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":false,"status":"` + string(status) + `"}`)
		event, err := client.ParseWebhook(raw)
		noError(t, err)
		equal(t, status, event.Status)
		isTrue(t, event.HasOutcome)
		isFalse(t, event.Success)
		isFalse(t, event.ChargeSucceeded())
		second, err := client.ParseWebhook(raw)
		noError(t, err)
		equal(t, event.EventID, second.EventID)
		equal(t, "P1", event.EventID)
		equal(t, sha256Hex(raw), event.PayloadSHA256)
	}
}

// A callback naming another merchant is not this merchant's business, and
// accepting it would let anyone's payload move this merchant's state.
func TestParseWebhookRejectsAnotherMerchant(t *testing.T) {
	_, client := newFake(t)
	_, err := client.ParseWebhook([]byte(`{"merchantId":"someone-else","purpose":"charge","success":true}`))
	hasError(t, err)
	errIs(t, err, ErrInvalidRequest)
	contains(t, err.Error(), "merchant")

	_, err = client.ParseWebhook([]byte(`{"purpose":"charge","success":true}`))
	errIs(t, err, ErrInvalidRequest)
}

func TestParseWebhookRejectsBodiesItCannotRead(t *testing.T) {
	_, client := newFake(t)
	for _, raw := range []string{`not json`, ``, `[1,2,3]`} {
		_, err := client.ParseWebhook([]byte(raw))
		hasError(t, err, raw)
		errIs(t, err, ErrInvalidRequest)
	}
}

func TestWebhookAckIsTheDocumentedAcknowledgement(t *testing.T) {
	recorder := httptest.NewRecorder()
	noError(t, WriteAck(recorder))
	equal(t, http.StatusOK, recorder.Code)
	equal(t, AckContentType, recorder.Header().Get("Content-Type"))
	equal(t, string(AckBody), recorder.Body.String())
}

func TestWebhookRejectsMissingIdentityOrOutcome(t *testing.T) {
	_, client := newFake(t)
	for _, field := range []string{"merchantId", "id", "purpose", "success", "token"} {
		for _, value := range []any{nil, "", map[string]any{}, 1} {
			t.Run(fmt.Sprintf("%s/%v", field, value), func(t *testing.T) {
				payload := map[string]any{"merchantId": "oentech", "id": "binding-1", "purpose": "token", "success": true, "token": "secret"}
				payload[field] = value
				raw, err := json.Marshal(payload)
				noError(t, err)
				event, err := client.ParseWebhook(raw)
				errIs(t, err, ErrInvalidRequest)
				equal(t, (*WebhookEvent)(nil), event)
			})
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"merchantId":"oentech","id":"x","purpose":"unknown","success":true}`} {
		_, err := client.ParseWebhook([]byte(raw))
		errIs(t, err, ErrInvalidRequest)
	}
}

func TestWebhookRejectsInvalidAmounts(t *testing.T) {
	_, client := newFake(t)
	for _, fields := range []string{`"amount":"bad"`, `"amount":0.5`, `"amount":9223372036854775808`, `"productDetails":[{"quantity":1,"unitPrice":"bad"}]`, `"productDetails":[{"quantity":1,"unitPrice":0.5}]`} {
		t.Run(fields, func(t *testing.T) {
			_, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,` + fields + `}`))
			errIs(t, err, ErrInvalidRequest)
		})
	}
}

func TestScalarPaymentInfoRedactsCardsButPreservesLinePay(t *testing.T) {
	_, client := newFake(t)
	for _, test := range []struct{ purpose, method, want string }{
		{"charge", "card", "4242"}, {"token", "", "4242"}, {"charge", "linePay", "4242424242424242"},
	} {
		t.Run(test.purpose+test.method, func(t *testing.T) {
			raw := []byte(`{"merchantId":"oentech","id":"P1","purpose":"` + test.purpose + `","success":true,"token":"secret","paymentMethod":"` + test.method + `","paymentInfo":"4242424242424242"}`)
			event, err := client.ParseWebhook(raw)
			noError(t, err)
			equal(t, test.want, event.PaymentInfo.Text)
			var payload map[string]any
			noError(t, json.Unmarshal(event.RedactedPayload, &payload))
			equal[any](t, test.want, payload["paymentInfo"])
			sanitized, err := SanitizeJSON(raw)
			noError(t, err)
			noError(t, json.Unmarshal(sanitized, &payload))
			equal[any](t, test.want, payload["paymentInfo"])
		})
	}
}
