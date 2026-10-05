package subscription

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	oen "github.com/howardsun-tw/oen-go"
)

// Webhook headers Oen sends with every delivery.
const (
	HeaderSignature       = "OenPay-Signature"
	HeaderEventType       = "OenPay-Event-Type"
	HeaderEventID         = "OenPay-Event-Id"
	HeaderDeliveryAttempt = "OenPay-Delivery-Attempt"
)

// DefaultWebhookTolerance is the largest accepted difference between the
// signature timestamp and the local clock. Oen sets no limit and recommends
// rejecting notifications older than 5 minutes.
const DefaultWebhookTolerance = 5 * time.Minute

const webhookSecretPrefix = "sub_whsec_"

// EventType names a webhook event. A type this SDK does not know is passed
// through unchanged.
type EventType string

const (
	EventSubscriptionCreated              EventType = "subscription_created"
	EventSubscriptionRenewed              EventType = "subscription_renewed"
	EventSubscriptionFailed               EventType = "subscription_failed"
	EventSubscriptionRecovered            EventType = "subscription_recovered"
	EventSubscriptionPaused               EventType = "subscription_paused"
	EventSubscriptionCancelled            EventType = "subscription_cancelled"
	EventSubscriptionResumed              EventType = "subscription_resumed"
	EventSubscriptionTerminated           EventType = "subscription_terminated"
	EventSubscriptionPlanChanged          EventType = "subscription_plan_changed"
	EventSubscriptionPeriodChanged        EventType = "subscription_period_changed"
	EventSubscriptionPaymentMethodUpdated EventType = "subscription_payment_method_updated"
	EventSubscriptionPaymentRefunded      EventType = "subscription_payment_refunded"
	EventSubscriptionTrialStarted         EventType = "subscription_trial_started"
	EventSubscriptionTrialEnding          EventType = "subscription_trial_ending"
	EventSubscriptionTrialEnded           EventType = "subscription_trial_ended"
	EventSubscriptionUpcomingCharge       EventType = "subscription_upcoming_charge"
	EventCustomerUpdated                  EventType = "customer_updated"
)

var (
	// ErrInvalidSignature means the OenPay-Signature header is missing,
	// malformed, outside the tolerance or does not match the body. Answer
	// with a non-2xx status and do not act on the body.
	ErrInvalidSignature = errors.New("oen subscription: webhook signature is not valid")

	// ErrInvalidEvent means the signature matched but the body is not an
	// event this SDK can read.
	ErrInvalidEvent = errors.New("oen subscription: webhook event is not valid")
)

// Event is one verified webhook notification.
type Event struct {
	// ID (evt_…) is the deduplication key: Oen may deliver an event more
	// than once.
	ID      string
	Type    EventType
	Created time.Time
	// Subscription is data.subscription when present.
	Subscription *EventSubscription
	// PaymentDetail is data.paymentDetail when present.
	PaymentDetail *PaymentDetail
	// DeliveryAttempt is the OenPay-Delivery-Attempt header, 0 when absent.
	DeliveryAttempt int
	// Data is the sanitized data object.
	Data json.RawMessage
}

// EventSubscription is the documented part of data.subscription.
type EventSubscription struct {
	ID     string
	Status Status
}

// PaymentDetail is the documented part of data.paymentDetail.
type PaymentDetail struct {
	Amount   int64
	Currency string
}

// WebhookVerifier checks and decodes webhook notifications. It holds no
// state between calls and is safe for concurrent use.
type WebhookVerifier struct {
	// Secret is the signing secret (sub_whsec_…) from the CRM webhook page.
	Secret string
	// Tolerance defaults to DefaultWebhookTolerance.
	Tolerance time.Duration
	// Now defaults to time.Now.
	Now func() time.Time
}

// Verify checks the signature of the raw request body and decodes it. Pass
// the body exactly as received; re-encoded JSON will not match.
//
// After a key rotation Oen sends two v1 signatures for 24 hours; one match
// is enough.
func (v WebhookVerifier) Verify(payload []byte, header http.Header) (*Event, error) {
	if err := v.VerifySignature(payload, header.Get(HeaderSignature)); err != nil {
		return nil, err
	}
	event, err := decodeEvent(payload)
	if err != nil {
		return nil, err
	}
	if attempt, err := strconv.Atoi(strings.TrimSpace(header.Get(HeaderDeliveryAttempt))); err == nil && attempt > 0 {
		event.DeliveryAttempt = attempt
	}
	return event, nil
}

// VerifySignature checks one OenPay-Signature value,
// "t=<unix seconds>,v1=<hex>[,v1=<hex>]", against the raw body. The signed
// message is "<t>.<body>" with HMAC-SHA256 under the signing secret.
func (v WebhookVerifier) VerifySignature(payload []byte, signature string) error {
	secret := strings.TrimSpace(v.Secret)
	if !strings.HasPrefix(secret, webhookSecretPrefix) {
		return fmt.Errorf("%w: signing secret must start with %s", ErrInvalidSignature, webhookSecretPrefix)
	}
	tolerance := v.Tolerance
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}

	var timestamp string
	var signatures [][]byte
	for _, part := range strings.Split(signature, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			if timestamp != "" {
				return fmt.Errorf("%w: more than one timestamp", ErrInvalidSignature)
			}
			timestamp = value
		case "v1":
			decoded, err := hex.DecodeString(value)
			if err == nil && len(decoded) == sha256.Size {
				signatures = append(signatures, decoded)
			}
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || timestamp == "" || timestamp[0] == '-' || timestamp[0] == '+' {
		return fmt.Errorf("%w: missing or malformed timestamp", ErrInvalidSignature)
	}
	if len(signatures) == 0 {
		return fmt.Errorf("%w: no v1 signature", ErrInvalidSignature)
	}
	age := now().Sub(time.Unix(seconds, 0))
	if age > tolerance || age < -tolerance {
		return fmt.Errorf("%w: timestamp is outside the %s tolerance", ErrInvalidSignature, tolerance)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	for _, candidate := range signatures {
		if hmac.Equal(candidate, expected) {
			return nil
		}
	}
	return fmt.Errorf("%w: no signature matches the body", ErrInvalidSignature)
}

func decodeEvent(payload []byte) (*Event, error) {
	var envelope struct {
		ID      json.RawMessage `json:"id"`
		Type    json.RawMessage `json:"type"`
		Created json.RawMessage `json:"created"`
		Data    json.RawMessage `json:"data"`
	}
	if err := decodeObject(payload, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	id, err := optionalString("id", envelope.ID)
	if err != nil || id == "" {
		return nil, fmt.Errorf("%w: id must be a nonempty string", ErrInvalidEvent)
	}
	eventType, err := optionalString("type", envelope.Type)
	if err != nil || eventType == "" {
		return nil, fmt.Errorf("%w: type must be a nonempty string", ErrInvalidEvent)
	}
	created, err := optionalTime("created", envelope.Created)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	event := &Event{ID: id, Type: EventType(eventType)}
	if created != nil {
		event.Created = *created
	}
	if isNull(envelope.Data) {
		return event, nil
	}

	var data struct {
		Subscription  json.RawMessage `json:"subscription"`
		PaymentDetail json.RawMessage `json:"paymentDetail"`
	}
	if err := decodeObject(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("%w: data: %v", ErrInvalidEvent, err)
	}
	if !isNull(data.Subscription) {
		subscription, err := decodeSubscription(data.Subscription)
		if err != nil {
			return nil, fmt.Errorf("%w: data.subscription: %v", ErrInvalidEvent, err)
		}
		event.Subscription = &EventSubscription{ID: subscription.ID, Status: subscription.Status}
	}
	if !isNull(data.PaymentDetail) {
		detail, err := decodePaymentDetail(data.PaymentDetail)
		if err != nil {
			return nil, fmt.Errorf("%w: data.paymentDetail: %v", ErrInvalidEvent, err)
		}
		event.PaymentDetail = detail
	}
	clean, err := oen.SanitizeJSON(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	event.Data = clean
	return event, nil
}

func decodePaymentDetail(raw json.RawMessage) (*PaymentDetail, error) {
	var fields struct {
		Amount   json.RawMessage `json:"amount"`
		Currency json.RawMessage `json:"currency"`
	}
	if err := decodeObject(raw, &fields); err != nil {
		return nil, err
	}
	detail := &PaymentDetail{}
	if !isNull(fields.Amount) {
		amount, err := strconv.ParseInt(strings.TrimSpace(string(fields.Amount)), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("amount %s is not a whole number", strings.TrimSpace(string(fields.Amount)))
		}
		detail.Amount = amount
	}
	currency, err := optionalString("currency", fields.Currency)
	if err != nil {
		return nil, err
	}
	detail.Currency = currency
	return detail, nil
}
