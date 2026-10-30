package oen

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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
		"isRetry":true,
		"failureCode":"exceeds_credit_limit",
		"scheduleStatus":"active",
		"productDetails":[{"productionCode":"P0001","description":"plan","quantity":1,"unit":"個","unitPrice":1000}]
	}`))
	noError(t, err)
	equal(t, ActionSubscription, event.Action)
	equal(t, "S20240412YOARH7GM", event.SubscriptionID)
	equal(t, 3, event.Period)
	equal(t, 12, event.NumberOfPeriods)
	hasTime(t, event.NextChargeAt)
	isTrue(t, event.IsRetry)
	equal(t, "exceeds_credit_limit", event.FailureCode)
	equal(t, "active", event.ScheduleStatus)
	equal(t, 1, len(event.Items))
	equal(t, "P0001", event.Items[0].ProductionCode)
	equal(t, Amount(1000), event.Items[0].UnitPrice)
}

// A valid callback always carries a boolean outcome. Status remains available
// independently so callers can distinguish an in-flight charge.
func TestWebhookKeepsStatusSeparateFromOutcome(t *testing.T) {
	_, client := newFake(t)
	// Each outcome of one transaction is its own event, so a later status
	// must not be dropped as a duplicate of an earlier one.
	seen := map[string]TransactionStatus{}
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
		equal(t, "", seen[event.EventID])
		seen[event.EventID] = status
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
	for _, raw := range []string{`null`, `{}`} {
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
		{"charge", "card", "4242"}, {"charge", "applePay", "4242"}, {"token", "", "4242"}, {"charge", "linePay", "4242424242424242"},
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

const testWebhookSecret = "pay_whsec_0123456789abcdef"

var webhookNow = time.Unix(1790000000, 0)

// The charge notification from Oen's signature guide. Its OenPay-Event-Id was
// computed outside Go with the guide's openssl command.
const (
	documentedCharge = `{"purpose":"charge","action":"onetime","merchantId":"ming","orderId":"A20260928001",` +
		`"id":"2HhndgEquCbDzC5OyVxWSGZmd2l","transactionId":"2HhndgEquCbDzC5OyVxWSGZmd2l",` +
		`"transactionHid":"P20260928AB12CD34","status":"charged","success":true,"paymentMethod":"card",` +
		`"amount":1200,"currency":"twd","message":""}`
	documentedChargeEventID = "evt_1b94797a17096373f023fcb7921e9a02"
)

func newSigningClient(t *testing.T, merchantID string) *Client {
	t.Helper()
	client, err := New(Config{
		Environment:   Testing,
		MerchantID:    merchantID,
		AuthToken:     "auth-token",
		WebhookSecret: testWebhookSecret,
		Now:           func() time.Time { return webhookNow },
	})
	noError(t, err)
	return client
}

// signWebhook follows Oen's documented algorithm independently of the SDK:
// HMAC-SHA256 over "{t}.{raw body}", hex encoded.
func signWebhook(secret string, timestamp int64, body string) string {
	return signWebhookAt(secret, strconv.FormatInt(timestamp, 10), body)
}

func signWebhookAt(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%s.%s", timestamp, body)
	return hex.EncodeToString(mac.Sum(nil))
}

func signedHeader(signature, eventID string) http.Header {
	header := http.Header{}
	header.Set(HeaderSignature, signature)
	if eventID != "" {
		header.Set(HeaderEventID, eventID)
	}
	return header
}

func TestVerifyWebhookAcceptsTheDocumentedNotification(t *testing.T) {
	client := newSigningClient(t, "ming")
	ts := webhookNow.Unix()
	// Literal header names, so a typo in the exported constants fails here.
	header := http.Header{}
	header.Set("OenPay-Signature", fmt.Sprintf("t=%d,v1=%s,v1=%s", ts,
		signWebhook("pay_whsec_other", ts, documentedCharge), signWebhook(testWebhookSecret, ts, documentedCharge)))
	header.Set("OenPay-Event-Id", documentedChargeEventID)
	header.Set("OenPay-Delivery-Attempt", "2")

	event, err := client.VerifyWebhook([]byte(documentedCharge), header)
	noError(t, err)
	isTrue(t, event.Verified)
	isTrue(t, event.ChargeSucceeded())
	equal(t, documentedChargeEventID, event.EventID)
	equal(t, "2HhndgEquCbDzC5OyVxWSGZmd2l", event.ID)
	equal(t, 2, event.DeliveryAttempt)
	// Computed with shasum -a 256 over the same body.
	equal(t, "54a8351e6b47657f22a110175dab9d1369747d1e2c0e09edf147d8d797318b1a", event.PayloadSHA256)

	// The event ID header is optional; the body alone yields the same key.
	event, err = client.VerifyWebhook([]byte(documentedCharge), signedHeader(header.Get(HeaderSignature), ""))
	noError(t, err)
	equal(t, documentedChargeEventID, event.EventID)
	equal(t, 0, event.DeliveryAttempt)
}

func TestVerifyWebhookRejectsForgedNotifications(t *testing.T) {
	ts := webhookNow.Unix()
	valid := signWebhook(testWebhookSecret, ts, documentedCharge)
	tests := map[string]struct {
		body      string
		signature string
		eventID   string
	}{
		"missing header":       {documentedCharge, "", ""},
		"no timestamp":         {documentedCharge, "v1=" + valid, ""},
		"no v1":                {documentedCharge, fmt.Sprintf("t=%d", ts), ""},
		"two timestamps":       {documentedCharge, fmt.Sprintf("t=%d,t=%d,v1=%s", ts, ts, valid), ""},
		"signed timestamp":     {documentedCharge, fmt.Sprintf("t=+%d,v1=%s", ts, signWebhookAt(testWebhookSecret, fmt.Sprintf("+%d", ts), documentedCharge)), ""},
		"tampered body":        {documentedCharge + " ", fmt.Sprintf("t=%d,v1=%s", ts, valid), ""},
		"rotated-out secret":   {documentedCharge, fmt.Sprintf("t=%d,v1=%s", ts, signWebhook("pay_whsec_old", ts, documentedCharge)), ""},
		"timestamp swapped":    {documentedCharge, fmt.Sprintf("t=%d,v1=%s", ts+1, valid), ""},
		"too old":              {documentedCharge, fmt.Sprintf("t=%d,v1=%s", ts-301, signWebhook(testWebhookSecret, ts-301, documentedCharge)), ""},
		"too far in future":    {documentedCharge, fmt.Sprintf("t=%d,v1=%s", ts+301, signWebhook(testWebhookSecret, ts+301, documentedCharge)), ""},
		"truncated v1":         {documentedCharge, fmt.Sprintf("t=%d,v1=%s", ts, valid[:62]), ""},
		"event id not in body": {documentedCharge, fmt.Sprintf("t=%d,v1=%s", ts, valid), "evt_00000000000000000000000000000000"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			event, err := newSigningClient(t, "ming").VerifyWebhook([]byte(tc.body), signedHeader(tc.signature, tc.eventID))
			errIs(t, err, ErrInvalidSignature)
			equal(t, (*WebhookEvent)(nil), event)
		})
	}
}

// Oen documents one v1 today, but any listed v1 that matches authenticates the
// body, so its position and unreadable neighbours must not matter.
func TestVerifyWebhookAcceptsAnyMatchingSignature(t *testing.T) {
	ts := webhookNow.Unix()
	valid := signWebhook(testWebhookSecret, ts, documentedCharge)
	wrong := signWebhook("pay_whsec_other", ts, documentedCharge)
	for name, signature := range map[string]string{
		"valid before a wrong one":     fmt.Sprintf("t=%d,v1=%s,v1=%s", ts, valid, wrong),
		"valid before a malformed one": fmt.Sprintf("t=%d,v1=%s,v1=zz", ts, valid),
		"malformed before a valid one": fmt.Sprintf("t=%d,v1=%s,v1=%s", ts, valid[:62], valid),
	} {
		t.Run(name, func(t *testing.T) {
			event, err := newSigningClient(t, "ming").VerifyWebhook([]byte(documentedCharge), signedHeader(signature, ""))
			noError(t, err)
			isTrue(t, event.Verified)
		})
	}
}

func TestWebhookToleranceIsConfigurable(t *testing.T) {
	// The default accepts exactly 5 minutes either way; 301 seconds is
	// rejected in TestVerifyWebhookRejectsForgedNotifications.
	for _, ts := range []int64{webhookNow.Unix() - 300, webhookNow.Unix() + 300} {
		header := signedHeader(fmt.Sprintf("t=%d,v1=%s", ts, signWebhook(testWebhookSecret, ts, documentedCharge)), "")
		_, err := newSigningClient(t, "ming").VerifyWebhook([]byte(documentedCharge), header)
		noError(t, err)
	}

	ts := webhookNow.Unix() - 600
	header := signedHeader(fmt.Sprintf("t=%d,v1=%s", ts, signWebhook(testWebhookSecret, ts, documentedCharge)), "")
	_, err := newSigningClient(t, "ming").VerifyWebhook([]byte(documentedCharge), header)
	errIs(t, err, ErrInvalidSignature)

	client, err := New(Config{
		Environment:      Testing,
		MerchantID:       "ming",
		AuthToken:        "auth-token",
		WebhookSecret:    testWebhookSecret,
		WebhookTolerance: 15 * time.Minute,
		Now:              func() time.Time { return webhookNow },
	})
	noError(t, err)
	_, err = client.VerifyWebhook([]byte(documentedCharge), header)
	noError(t, err)
}

// Without a secret every signature is computable by anyone, so verification
// must refuse to run rather than report success or fall back to parsing.
func TestVerifyWebhookRequiresASecret(t *testing.T) {
	_, client := newFake(t)
	ts := webhookNow.Unix()
	header := signedHeader(fmt.Sprintf("t=%d,v1=%s", ts, signWebhook("", ts, documentedCharge)), "")
	_, err := client.VerifyWebhook([]byte(documentedCharge), header)
	errIs(t, err, ErrInvalidInput)
	errIsNot(t, err, ErrInvalidSignature)
}

// VerifyWebhookSignature never reads the body, so it authenticates one that
// VerifyWebhook cannot parse; the two failures must stay distinguishable.
func TestVerifyWebhookSignatureAuthenticatesAnUnreadableBody(t *testing.T) {
	client := newSigningClient(t, "ming")
	body := `not json`
	ts := webhookNow.Unix()
	signature := fmt.Sprintf("t=%d,v1=%s", ts, signWebhook(testWebhookSecret, ts, body))
	noError(t, client.VerifyWebhookSignature([]byte(body), signature))
	_, err := client.VerifyWebhook([]byte(body), signedHeader(signature, ""))
	errIs(t, err, ErrInvalidRequest)
	errIsNot(t, err, ErrInvalidSignature)
}

// Oen asks receivers to skip purposes they do not know and still answer 2xx,
// so an authentic unknown purpose must not look like a forgery or a bad body.
func TestUnknownPurposesParseWithCommonFields(t *testing.T) {
	client := newSigningClient(t, "ming")
	// interval, isRetry and amount mean something else, or nothing, here.
	body := `{"purpose":"payout_settled","merchantId":"ming","id":"payout#1","orderId":"A1",` +
		`"amount":"nine hundred","interval":"weekly","isRetry":"no","success":"maybe"}`
	ts := webhookNow.Unix()
	event, err := client.VerifyWebhook([]byte(body), signedHeader(fmt.Sprintf("t=%d,v1=%s", ts, signWebhook(testWebhookSecret, ts, body)), ""))
	noError(t, err)
	isTrue(t, event.Verified)
	isFalse(t, event.Purpose.Known())
	equal(t, "payout#1", event.ID)
	isFalse(t, event.HasOutcome)
	isFalse(t, event.ChargeSucceeded())
	equal(t, "A1", event.OrderID)
	isFalse(t, event.HasAmount)
	contains(t, string(event.RedactedPayload), `"interval":"weekly"`)

	// Field names a known purpose types strictly may hold anything here.
	// The event ID was computed with Node.js 24.
	event, err = client.ParseWebhook([]byte(`{"purpose":"payout_settled","merchantId":"ming","id":"payout#1","productDetails":{},"paymentInfo":[1]}`))
	noError(t, err)
	equal(t, "evt_807ed1c03ee198774c7d02c4a0f2966d", event.EventID)

	_, err = client.ParseWebhook([]byte(`{"purpose":"payout_settled","merchantId":"someone-else","id":"payout#1"}`))
	errIs(t, err, ErrInvalidRequest)
}

// The examples below are Oen's own, from the webhook object page. Their event
// IDs were computed with Node.js 24 from the documented formula.
func TestChargeInProgressHasNoOutcome(t *testing.T) {
	client := newSigningClient(t, "ming")
	event, err := client.ParseWebhook([]byte(`{"purpose":"charge","action":"onetime","merchantId":"ming",` +
		`"orderId":"A20260928002","id":"2HhnkQ3vXy8LpA1cB0dE9fG2hIj","transactionId":"2HhnkQ3vXy8LpA1cB0dE9fG2hIj",` +
		`"transactionHid":"P20260928UV12WX34","status":"charging","paymentMethod":"cvs","amount":500,"currency":"twd",` +
		`"paymentInfo":{"cvsName":"超商代碼繳費","code":"XXXXXXXXXXXX","expiredAt":"2026-09-30T02:00:00.000Z"},"message":""}`))
	noError(t, err)
	equal(t, StatusCharging, event.Status)
	isFalse(t, event.HasOutcome)
	isFalse(t, event.ChargeSucceeded())
	equal(t, "evt_8a550afdd40eab92497e14072e96191c", event.EventID)

	// Present but not a boolean is still a malformed outcome.
	for _, success := range []string{`1`, `""`, `"true"`} {
		_, err = client.ParseWebhook([]byte(`{"purpose":"charge","merchantId":"ming","id":"tx","status":"charged","success":` + success + `}`))
		errIs(t, err, ErrInvalidRequest)
	}
}

func TestParseWebhookReadsAScheduledSubscription(t *testing.T) {
	client := newSigningClient(t, "ming")
	event, err := client.ParseWebhook([]byte(`{"purpose":"schedule_subscription","merchantId":"ming",` +
		`"id":"2HhnfZ1kqRmA7cX0TQwq8vBn3Ls","subscriptionId":"S20260928EF56GH78","success":true,"amount":1500,` +
		`"currency":"twd","period":0,"numberOfPeriods":4,"interval":3,"startedAt":"2026-10-15T00:00:00.000Z",` +
		`"nextChargeAt":"2026-10-15T01:00:00.000Z","paymentInfo":"4242","message":""}`))
	noError(t, err)
	isTrue(t, event.Purpose.Known())
	isTrue(t, event.HasOutcome)
	isTrue(t, event.Success)
	isFalse(t, event.ChargeSucceeded())
	equal(t, "S20260928EF56GH78", event.SubscriptionID)
	equal(t, Amount(1500), event.Amount)
	equal(t, 4, event.NumberOfPeriods)
	equal(t, 3, event.Interval)
	equal(t, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), *event.StartedAt)
	equal(t, "4242", event.PaymentInfo.CardLast4)
	equal(t, "evt_3d26d9f434082d843600198c85d97b80", event.EventID)

	failed, err := client.ParseWebhook([]byte(`{"purpose":"schedule_subscription","merchantId":"ming",` +
		`"id":"s2","subscriptionId":"S2","success":false,"message":"3DS_ABANDONED"}`))
	noError(t, err)
	isTrue(t, failed.HasOutcome)
	isFalse(t, failed.Success)
	equal(t, "3DS_ABANDONED", failed.Message)
}

// paymentInfo on these purposes is always card data, so even a spaced card
// number, which does not look like a PAN to the generic check, is reduced.
func TestCardPurposesNeverReturnAFullCardNumber(t *testing.T) {
	client := newSigningClient(t, "ming")
	for _, body := range []string{
		`{"purpose":"subscription_card_changed","merchantId":"ming","id":"cc1","success":true,"status":"succeeded","subscriptionId":"S1","paymentInfo":"4242 4242 4242 4242"}`,
		`{"purpose":"schedule_subscription","merchantId":"ming","id":"s1","success":true,"subscriptionId":"S1","paymentInfo":"4242 4242 4242 4242"}`,
	} {
		event, err := client.ParseWebhook([]byte(body))
		noError(t, err)
		equal(t, "4242", event.PaymentInfo.CardLast4)
		notContains(t, event.PaymentInfo.Text, "4242 4242")
		notContains(t, string(event.RedactedPayload), "4242 4242")
	}
	// A card number printed in groups, or padded, under an undocumented
	// nested key.
	for _, pan := range []string{"4242 4242 4242 4242", "4242-4242-4242-4242", `\t4242424242424242\n`} {
		for _, body := range []string{
			`{"purpose":"schedule_subscription","merchantId":"ming","id":"s1","success":true,"subscriptionId":"S1","paymentInfo":{"number":"` + pan + `"}}`,
			`{"purpose":"charge","merchantId":"ming","id":"tx","paymentMethod":"card","paymentInfo":{"number":"` + pan + `"}}`,
		} {
			event, err := client.ParseWebhook([]byte(body))
			noError(t, err)
			notContains(t, string(event.RedactedPayload), "42424242")
			notContains(t, string(event.RedactedPayload), "4242 4242")
			notContains(t, string(event.RedactedPayload), "4242-4242")
			contains(t, string(event.RedactedPayload), `"number":"4242"`)
		}
	}
	// A last group shorter than four digits still leaves four digits.
	for pan, want := range map[string]string{"4000-0000-0000-0000-006": "0006", "4222 2222 2222 2": "2222"} {
		event, err := client.ParseWebhook([]byte(`{"purpose":"charge","merchantId":"ming","id":"tx","paymentMethod":"card",` +
			`"paymentInfo":{"number":"` + pan + `","cardNum":"` + pan + `"}}`))
		noError(t, err)
		contains(t, string(event.RedactedPayload), `"number":"`+want+`"`)
		contains(t, string(event.RedactedPayload), `"cardNum":"`+want+`"`)
		equal(t, want, event.PaymentInfo.CardLast4)
	}
}

// A bank account can pass the card number check. Where the payload does not
// say it is a card payment, a grouped account must stay whole; an ungrouped
// Luhn-valid number is still masked, whatever a nested method claims.
func TestNonCardPaymentInfoKeepsGroupedAccounts(t *testing.T) {
	client := newSigningClient(t, "ming")
	event, err := client.ParseWebhook([]byte(`{"purpose":"charge","merchantId":"ming","id":"tx",` +
		`"paymentInfo":{"bankCode":"812","account":"1234-5678-9012-3452"}}`))
	noError(t, err)
	equal(t, "1234-5678-9012-3452", event.PaymentInfo.Account)
	contains(t, string(event.RedactedPayload), "1234-5678-9012-3452")

	for _, method := range []string{"atm", "credit"} {
		event, err := client.ParseWebhook([]byte(`{"purpose":"charge","merchantId":"ming","id":"tx",` +
			`"paymentInfo":{"method":"` + method + `","number":"4242424242424242"}}`))
		noError(t, err)
		notContains(t, string(event.RedactedPayload), "4242424242424242")
	}
}

func TestParseWebhookReadsAnIssuedInvoice(t *testing.T) {
	client := newSigningClient(t, "ming")
	event, err := client.ParseWebhook([]byte(`{"purpose":"invoice_issued",` +
		`"id":"invoice#2HhnqT4cWz0mR8bLxN3vYs6KdPa#issued","transactionId":"invoice#2HhnqT4cWz0mR8bLxN3vYs6KdPa#issued",` +
		`"merchantId":"ming","chargeId":"2HhndgEquCbDzC5OyVxWSGZmd2l","orderId":"A20260928001","invoiceNumber":"ZZ00001234",` +
		`"invoiceStatus":"issued","invoiceDate":"2026-09-28T02:41:30.000Z","randomNumber":"0000","invoiceAmount":1200,` +
		`"createdAt":"2026-09-28T02:41:31.204Z","message":""}`))
	noError(t, err)
	isTrue(t, event.Purpose.Known())
	equal(t, "2HhndgEquCbDzC5OyVxWSGZmd2l", event.ChargeID)
	equal(t, "A20260928001", event.OrderID)
	equal(t, &WebhookInvoice{
		Number:       "ZZ00001234",
		Status:       "issued",
		Date:         timePtr(time.Date(2026, 9, 28, 2, 41, 30, 0, time.UTC)),
		RandomNumber: "0000",
		Amount:       1200,
		HasAmount:    true,
	}, event.Invoice)
	equal(t, time.Date(2026, 9, 28, 2, 41, 31, 204000000, time.UTC), *event.CreatedAt)
	equal(t, "evt_8e1d22be09a2c26d63df02c398325357", event.EventID)
	isTrue(t, event.Refund == nil && event.Retry == nil && event.Cancellation == nil)
}

// invoiceAmount is always sent but may be null when the invoice items are
// incomplete; null and zero must stay distinguishable.
func TestParseWebhookReadsAVoidedInvoice(t *testing.T) {
	client := newSigningClient(t, "ming")
	for amount, want := range map[string]WebhookInvoice{
		`null`: {Number: "ZZ00001234", Status: "voided", RandomNumber: "0000"},
		`0`:    {Number: "ZZ00001234", Status: "voided", RandomNumber: "0000", HasAmount: true},
	} {
		event, err := client.ParseWebhook([]byte(`{"purpose":"invoice_voided","id":"invoice#1#voided","merchantId":"ming",` +
			`"chargeId":"c1","invoiceNumber":"ZZ00001234","invoiceStatus":"voided","invoiceDate":"2026-09-28T02:41:30.000Z",` +
			`"randomNumber":"0000","invoiceAmount":` + amount + `,"createdAt":"2026-09-29T00:00:00.000Z"}`))
		noError(t, err)
		equal(t, PurposeInvoiceVoided, event.Purpose)
		want.Date = timePtr(time.Date(2026, 9, 28, 2, 41, 30, 0, time.UTC))
		equal(t, &want, event.Invoice, amount)
	}
}

// Oen's tables for these three list no merchantId, so its absence must not
// drop an authentic event; a merchantId naming someone else still does.
func TestParseWebhookReadsTheOptInSubscriptionAndRefundEvents(t *testing.T) {
	client := newSigningClient(t, "ming")

	refund, err := client.ParseWebhook([]byte(`{"purpose":"refund","id":"refund#r1#refunded","refundId":"r1",` +
		`"chargeId":"c1","chargeHumanId":"P1","amount":300,"origAmount":1200,"currency":"twd","refundStatus":"refunded",` +
		`"reason":"customer request","requestedAt":"2026-10-01T00:00:00.000Z","resolvedAt":"2026-10-02T00:00:00.000Z"}`))
	noError(t, err)
	isTrue(t, refund.Purpose.Known())
	equal(t, Amount(300), refund.Amount)
	equal(t, "P1", refund.ChargeHID)
	equal(t, "customer request", refund.Reason)
	equal(t, &WebhookRefund{
		RefundID:       "r1",
		Status:         "refunded",
		OriginalAmount: 1200,
		RequestedAt:    timePtr(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
		ResolvedAt:     timePtr(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)),
	}, refund.Refund)

	cancelled, err := client.ParseWebhook([]byte(`{"purpose":"subscription_cancelled","id":"cancel#S1",` +
		`"subscriptionId":"S1","source":"crm","reason":"moved","cancelledAt":"2026-10-03T00:00:00.000Z",` +
		`"cancelledFromStatus":"active","period":2,"numberOfPeriods":12}`))
	noError(t, err)
	isTrue(t, cancelled.Purpose.Known())
	equal(t, 2, cancelled.Period)
	equal(t, &WebhookCancellation{
		Source:              "crm",
		CancelledAt:         timePtr(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)),
		CancelledFromStatus: "active",
	}, cancelled.Cancellation)

	retry, err := client.ParseWebhook([]byte(`{"purpose":"subscription_retry","id":"retry#S1#2","outcome":"scheduled",` +
		`"retryAttemptNumber":2,"nextRetryAt":"2026-10-05T01:00:00.000Z","failureCode":"exceeds_credit_limit",` +
		`"chargeId":"c2","subscriptionId":"S1","period":3}`))
	noError(t, err)
	isTrue(t, retry.Purpose.Known())
	equal(t, "exceeds_credit_limit", retry.FailureCode)
	equal(t, "c2", retry.ChargeID)
	equal(t, &WebhookRetry{
		Outcome:       "scheduled",
		AttemptNumber: 2,
		NextRetryAt:   timePtr(time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)),
	}, retry.Retry)

	for _, purpose := range []WebhookPurpose{PurposeRefund, PurposeSubscriptionCancelled, PurposeSubscriptionRetry} {
		_, err = client.ParseWebhook([]byte(`{"purpose":"` + string(purpose) + `","id":"e1","merchantId":"ming"}`))
		if err != nil {
			t.Fatalf("%v: %v", purpose, err)
		}
		_, err = client.ParseWebhook([]byte(`{"purpose":"` + string(purpose) + `","id":"e1","merchantId":"someone-else"}`))
		errIs(t, err, ErrInvalidRequest)
	}
}

func TestParseWebhookReadsACardChange(t *testing.T) {
	client := newSigningClient(t, "ming")
	event, err := client.ParseWebhook([]byte(`{"purpose":"subscription_card_changed","id":"cc1","transactionId":"cc1",` +
		`"merchantId":"ming","success":true,"status":"succeeded","subscriptionId":"S1","paymentInfo":"1881",` +
		`"cardBrand":"visa","userId":"member-1"}`))
	noError(t, err)
	isTrue(t, event.Purpose.Known())
	isTrue(t, event.HasOutcome && event.Success)
	equal(t, TransactionStatus("succeeded"), event.Status)
	equal(t, "S1", event.SubscriptionID)
	equal(t, "visa", event.CardBrand)
	equal(t, "1881", event.PaymentInfo.CardLast4)
	equal(t, "member-1", event.Customer.ID)
}

// Each documented purpose has fields Oen always sends. Every case removes one
// field from an otherwise valid body, so only that requirement can reject it.
func TestWebhookRejectsPurposeBodiesMissingRequiredFields(t *testing.T) {
	client := newSigningClient(t, "ming")
	valid := map[WebhookPurpose]string{
		PurposeCharge:                  `{"purpose":"charge","merchantId":"ming","id":"tx","status":"charged","success":true}`,
		PurposeToken:                   `{"purpose":"token","merchantId":"ming","id":"b1","success":false}`,
		PurposeScheduleSubscription:    `{"purpose":"schedule_subscription","merchantId":"ming","id":"s","success":true,"subscriptionId":"S1"}`,
		PurposeSubscriptionCardChanged: `{"purpose":"subscription_card_changed","merchantId":"ming","id":"c","success":true,"status":"succeeded","subscriptionId":"S1"}`,
		PurposeRefund:                  `{"purpose":"refund","id":"r"}`,
		PurposeSubscriptionCancelled:   `{"purpose":"subscription_cancelled","id":"c"}`,
		PurposeSubscriptionRetry:       `{"purpose":"subscription_retry","id":"r"}`,
		PurposeInvoiceIssued: `{"purpose":"invoice_issued","merchantId":"ming","id":"i","chargeId":"c","invoiceNumber":"ZZ1",` +
			`"invoiceStatus":"issued","invoiceDate":"2026-09-28T02:41:30.000Z","randomNumber":"0000","invoiceAmount":null,"createdAt":"2026-09-28T02:41:31.204Z"}`,
	}
	valid[PurposeInvoiceVoided] = strings.Replace(valid[PurposeInvoiceIssued], `"invoice_issued"`, `"invoice_voided"`, 1)
	// Copied from Oen's field tables (一定有), not from webhookRequiredFields,
	// so dropping a requirement from the SDK fails here.
	invoice := []string{"merchantId", "id", "chargeId", "invoiceNumber", "invoiceStatus", "invoiceDate", "randomNumber", "invoiceAmount", "createdAt"}
	required := map[WebhookPurpose][]string{
		PurposeCharge:                  {"merchantId", "id"},
		PurposeToken:                   {"merchantId", "id", "success"},
		PurposeScheduleSubscription:    {"merchantId", "id", "success", "subscriptionId"},
		PurposeSubscriptionCardChanged: {"merchantId", "id", "success", "status", "subscriptionId"},
		PurposeRefund:                  {"id"},
		PurposeSubscriptionCancelled:   {"id"},
		PurposeSubscriptionRetry:       {"id"},
		PurposeInvoiceIssued:           invoice,
		PurposeInvoiceVoided:           invoice,
	}
	for purpose, body := range valid {
		_, err := client.ParseWebhook([]byte(body))
		if err != nil {
			t.Fatalf("%v: %v", purpose, err)
		}
		for _, field := range required[purpose] {
			t.Run(string(purpose)+"/"+field, func(t *testing.T) {
				var payload map[string]json.RawMessage
				noError(t, json.Unmarshal([]byte(body), &payload))
				delete(payload, field)
				missing, err := json.Marshal(payload)
				noError(t, err)
				event, err := client.ParseWebhook(missing)
				errIs(t, err, ErrInvalidRequest)
				contains(t, err.Error(), field)
				equal(t, (*WebhookEvent)(nil), event)
			})
		}
	}
}

func TestWebhookRejectsMalformedPurposeFields(t *testing.T) {
	client := newSigningClient(t, "ming")
	for name, body := range map[string]string{
		"retry with fractional attempt":     `{"purpose":"subscription_retry","id":"r","retryAttemptNumber":1.5}`,
		"invoice with fractional amount":    `{"purpose":"invoice_issued","merchantId":"ming","id":"i","chargeId":"c","invoiceNumber":"ZZ1","invoiceStatus":"issued","invoiceDate":"2026-09-28T02:41:30.000Z","randomNumber":"0000","invoiceAmount":12.5,"createdAt":"2026-09-28T02:41:31.204Z"}`,
		"refund with unreadable resolvedAt": `{"purpose":"refund","id":"r","resolvedAt":"yesterday"}`,
		"charge with non-boolean isRetry":   `{"purpose":"charge","merchantId":"ming","id":"tx","isRetry":"yes"}`,
		"numeric status on a known purpose": `{"purpose":"charge","merchantId":"ming","id":"tx","status":1}`,
		"upper-case ID instead of id":       `{"purpose":"token","merchantId":"ming","ID":"tx","success":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			event, err := client.ParseWebhook([]byte(body))
			errIs(t, err, ErrInvalidRequest)
			equal(t, (*WebhookEvent)(nil), event)
		})
	}
}

// The event ID must match JavaScript's JSON.stringify of JSON.parse'd values
// byte for byte. Each expected value was computed with Node.js 24 from the
// same body.
func TestEventIDMatchesJavaScriptEncoding(t *testing.T) {
	client := newSigningClient(t, "oentech")
	for body, want := range map[string]string{
		// U+2028, HTML characters, short escapes, control characters,
		// DEL, CJK and surrounding spaces.
		`{"merchantId":"oentech","purpose":"token","success":false,` +
			`"id":" s-1\u2028<&>\"\\\b\f\n\r\t\u0001\u007f中"}`: "evt_40fd913a6e085cf2e21196744ae68ff3",
		// JavaScript reads the exact key "id"; "ID" is another property.
		`{"purpose":"token","merchantId":"ming","id":"first","ID":"second","success":false}`: "evt_80463d0a3109086129e353083f253e57",
		// A lone surrogate survives JSON.parse and is escaped again.
		`{"purpose":"token","merchantId":"ming","id":"\ud800","success":false}`:        "evt_6c99ac6e957ac73681e30a7cd7b84379",
		`{"purpose":"token","merchantId":"ming","id":"x\udc00\ud800","success":false}`: "evt_2dfd3afbdc934450764621d71295ed4f",
		// U+2029, an escaped surrogate pair and an escaped slash.
		`{"purpose":"token","merchantId":"ming","id":"a\u2029b\ud83d\ude00c\/d","success":false}`: "evt_c831818fa8f55f8ce024f8f648099acd",
	} {
		merchant := client
		if !strings.Contains(body, `"oentech"`) {
			merchant = newSigningClient(t, "ming")
		}
		event, err := merchant.ParseWebhook([]byte(body))
		if err != nil {
			t.Fatalf("%v: %v", body, err)
		}
		equal(t, want, event.EventID, body)
		if strings.Contains(body, `"ID"`) {
			equal(t, "first", event.ID)
		}
	}
}

// JSON.stringify writes numbers as Number.prototype.toString does, which an
// unknown purpose may carry in an identity slot. Each expected value was
// computed with Node.js 24 from the same body.
func TestEventIDMatchesJavaScriptNumbers(t *testing.T) {
	client := newSigningClient(t, "ming")
	for status, want := range map[string]string{
		"1":                    "evt_c91e9865396ef3e2b786481e1127e7e6",
		"-1":                   "evt_98bc85710e3e39970ae9397333b20a29",
		"1.25":                 "evt_60eb89d136db0fca2da9fc4133b06180",
		"1.0":                  "evt_c91e9865396ef3e2b786481e1127e7e6",
		"-0":                   "evt_a7b40b7296b2f37e07dc1209a63a65cb",
		"1e-400":               "evt_a7b40b7296b2f37e07dc1209a63a65cb",
		"0.1":                  "evt_52190ed8365645877deb3e98e6be81dd",
		"2.5E+3":               "evt_5cb5f8fe1279b244d434abc3918cfbb0",
		"0.000001":             "evt_adcd44b2a00a2bff262117382a867062",
		"1.5e-7":               "evt_d32103910a0c7f8d83457c6cf7bef3b8",
		"1e20":                 "evt_ab53a43f5339e313a7a3cd94b9852218",
		"1e21":                 "evt_6b7c0ec838d9c4ff2dfd586a9920bab0",
		"12345678901234567890": "evt_b63ee814a4c3b7fe284ffd8dda3616fa",
		"9007199254740993":     "evt_f2c8a3280ffecab3e6d1d355622b326e",
		"1e400":                "evt_807ed1c03ee198774c7d02c4a0f2966d",
		"-1e400":               "evt_807ed1c03ee198774c7d02c4a0f2966d",
	} {
		event, err := client.ParseWebhook([]byte(`{"purpose":"payout_settled","merchantId":"ming","id":"payout#1","status":` + status + `}`))
		if err != nil {
			t.Fatalf("%v: %v", status, err)
		}
		equal(t, want, event.EventID, status)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
