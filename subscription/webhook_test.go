package subscription

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const testSecret = "sub_whsec_0123456789abcdef"

var webhookNow = time.Unix(1790000000, 0)

// sign follows Oen's documented algorithm independently of the SDK:
// HMAC-SHA256 over "{t}.{raw body}", hex encoded.
func sign(secret string, timestamp int64, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", timestamp, body)
	return hex.EncodeToString(mac.Sum(nil))
}

func verifier() WebhookVerifier {
	return WebhookVerifier{Secret: testSecret, Now: func() time.Time { return webhookNow }}
}

func headerFor(signature string) http.Header {
	header := http.Header{}
	header.Set(HeaderSignature, signature)
	return header
}

const renewedEvent = `{"id":"evt_1","type":"subscription_renewed","created":1790000000,` +
	`"data":{"subscription":{"id":"sub_1","status":"active"},"paymentDetail":{"amount":299,"currency":"twd"}}}`

func TestWebhookWithAValidSignatureIsDecoded(t *testing.T) {
	ts := webhookNow.Unix()
	header := headerFor(fmt.Sprintf("t=%d,v1=%s", ts, sign(testSecret, ts, renewedEvent)))
	header.Set(HeaderDeliveryAttempt, "2")
	event, err := verifier().Verify([]byte(renewedEvent), header)
	noError(t, err)
	equal(t, "evt_1", event.ID)
	equal(t, EventSubscriptionRenewed, event.Type)
	equal(t, webhookNow.Unix(), event.Created.Unix())
	equal(t, &EventSubscription{ID: "sub_1", Status: StatusActive}, event.Subscription)
	equal(t, &PaymentDetail{Amount: 299, Currency: "twd"}, event.PaymentDetail)
	equal(t, 2, event.DeliveryAttempt)
	contains(t, string(event.Data), `"paymentDetail"`)
}

func TestWebhookAcceptsEitherSignatureDuringRotation(t *testing.T) {
	ts := webhookNow.Unix()
	old := sign("sub_whsec_old", ts, renewedEvent)
	current := sign(testSecret, ts, renewedEvent)
	for _, signature := range []string{
		fmt.Sprintf("t=%d,v1=%s,v1=%s", ts, old, current),
		fmt.Sprintf("v1=%s, t=%d , v1=%s", current, ts, old),
	} {
		_, err := verifier().Verify([]byte(renewedEvent), headerFor(signature))
		noError(t, err)
	}
}

func TestWebhookSignatureFailures(t *testing.T) {
	ts := webhookNow.Unix()
	valid := sign(testSecret, ts, renewedEvent)
	tests := map[string]struct {
		body      string
		signature string
		secret    string
	}{
		"missing header":     {renewedEvent, "", testSecret},
		"no timestamp":       {renewedEvent, "v1=" + valid, testSecret},
		"no v1":              {renewedEvent, fmt.Sprintf("t=%d", ts), testSecret},
		"v0 only":            {renewedEvent, fmt.Sprintf("t=%d,v0=%s", ts, valid), testSecret},
		"two timestamps":     {renewedEvent, fmt.Sprintf("t=%d,t=%d,v1=%s", ts, ts, valid), testSecret},
		"signed timestamp":   {renewedEvent, fmt.Sprintf("t=+%d,v1=%s", ts, valid), testSecret},
		"tampered body":      {renewedEvent + " ", fmt.Sprintf("t=%d,v1=%s", ts, valid), testSecret},
		"other secret":       {renewedEvent, fmt.Sprintf("t=%d,v1=%s", ts, sign("sub_whsec_x", ts, renewedEvent)), testSecret},
		"timestamp swapped":  {renewedEvent, fmt.Sprintf("t=%d,v1=%s", ts+1, valid), testSecret},
		"too old":            {renewedEvent, fmt.Sprintf("t=%d,v1=%s", ts-301, sign(testSecret, ts-301, renewedEvent)), testSecret},
		"too far in future":  {renewedEvent, fmt.Sprintf("t=%d,v1=%s", ts+301, sign(testSecret, ts+301, renewedEvent)), testSecret},
		"truncated v1":       {renewedEvent, fmt.Sprintf("t=%d,v1=%s", ts, valid[:62]), testSecret},
		"secret not sub_wh":  {renewedEvent, fmt.Sprintf("t=%d,v1=%s", ts, sign("whsec_x", ts, renewedEvent)), "whsec_x"},
		"empty secret":       {renewedEvent, fmt.Sprintf("t=%d,v1=%s", ts, sign("", ts, renewedEvent)), ""},
		"non-numeric stamp":  {renewedEvent, "t=abc,v1=" + valid, testSecret},
		"uppercase key name": {renewedEvent, fmt.Sprintf("T=%d,V1=%s", ts, valid), testSecret},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			v := verifier()
			v.Secret = tc.secret
			_, err := v.Verify([]byte(tc.body), headerFor(tc.signature))
			errIs(t, err, ErrInvalidSignature)
		})
	}
}

func TestWebhookToleranceIsConfigurable(t *testing.T) {
	ts := webhookNow.Unix() - 600
	signature := "t=" + strconv.FormatInt(ts, 10) + ",v1=" + sign(testSecret, ts, renewedEvent)
	_, err := verifier().Verify([]byte(renewedEvent), headerFor(signature))
	errIs(t, err, ErrInvalidSignature)

	v := verifier()
	v.Tolerance = 15 * time.Minute
	_, err = v.Verify([]byte(renewedEvent), headerFor(signature))
	noError(t, err)
}

func TestSignedButMalformedEventsAreRejected(t *testing.T) {
	ts := webhookNow.Unix()
	for _, body := range []string{
		`not json`,
		`[]`,
		`{"type":"subscription_renewed"}`,
		`{"id":"","type":"subscription_renewed"}`,
		`{"id":"evt_1"}`,
		`{"id":"evt_1","type":7}`,
		`{"id":"evt_1","type":"customer_updated","created":"yesterday"}`,
		`{"id":"evt_1","type":"customer_updated","data":[]}`,
		`{"id":"evt_1","type":"subscription_renewed","data":{"subscription":{"id":1}}}`,
		`{"id":"evt_1","type":"subscription_renewed","data":{"paymentDetail":{"amount":"299"}}}`,
		`{"id":"evt_1","type":"subscription_renewed","data":{"paymentDetail":{"amount":2.5}}}`,
	} {
		_, err := verifier().Verify([]byte(body), headerFor(fmt.Sprintf("t=%d,v1=%s", ts, sign(testSecret, ts, body))))
		errIs(t, err, ErrInvalidEvent)
		errIsNot(t, err, ErrInvalidSignature)
	}
}

func TestUnknownEventTypesAndMissingDataPassThrough(t *testing.T) {
	ts := webhookNow.Unix()
	body := `{"id":"evt_2","type":"subscription_future_thing","created":1790000000}`
	event, err := verifier().Verify([]byte(body), headerFor(fmt.Sprintf("t=%d,v1=%s", ts, sign(testSecret, ts, body))))
	noError(t, err)
	equal(t, EventType("subscription_future_thing"), event.Type)
	isTrue(t, event.Subscription == nil)
	isTrue(t, event.PaymentDetail == nil)
	equal(t, 0, event.DeliveryAttempt)
}

func TestWebhookDataIsSanitized(t *testing.T) {
	ts := webhookNow.Unix()
	body := `{"id":"evt_3","type":"subscription_payment_method_updated",` +
		`"data":{"paymentMethod":{"cardNumber":"4111111111111111","token":"tok_x"}}}`
	event, err := verifier().Verify([]byte(body), headerFor(fmt.Sprintf("t=%d,v1=%s", ts, sign(testSecret, ts, body))))
	noError(t, err)
	notContains(t, string(event.Data), "4111111111111111")
	notContains(t, string(event.Data), "tok_x")
}
