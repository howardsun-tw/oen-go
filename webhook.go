package oen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The acknowledgement Oen expects. Oen retries a failed webhook three times,
// two, four and six seconds apart, so an endpoint that cannot answer 200
// quickly will be called again.
var (
	AckStatus      = http.StatusOK
	AckContentType = "application/json"
	AckBody        = []byte(`{"received":true}`)
)

// WebhookPurpose is why Oen sent the callback.
type WebhookPurpose string

const (
	// PurposeCharge is a payment result.
	PurposeCharge WebhookPurpose = "charge"
	// PurposeToken is the result of a card binding started by
	// [Client.CreateTokenCheckout].
	PurposeToken WebhookPurpose = "token"
)

// WebhookEvent is one parsed Oen callback.
//
// Parsing is not authentication. Oen publishes no webhook signature, so
// Verified is always false and this event alone never proves that money moved.
// Confirm anything that matters with [Client.GetTransaction] or
// [Client.ListOrderTransactions] before changing state.
type WebhookEvent struct {
	// Verified reports whether the SDK authenticated the callback. It is
	// always false today; the field exists so callers write the check now.
	Verified bool

	MerchantID string
	Purpose    WebhookPurpose

	// Success is Oen's outcome flag. Read it together with HasOutcome.
	Success bool
	// HasOutcome is true on parsed events: a boolean success is required.
	HasOutcome bool

	// EventID is the provider transaction or binding identifier. Use it with
	// the event state when de-duplicating callbacks for different outcomes.
	EventID string
	// PayloadSHA256 is the digest of the raw body, for audit trails.
	PayloadSHA256 string

	// ID is Oen's id field: the transaction ID on a charge callback, the
	// binding session ID on a token callback.
	ID             string
	TransactionHID string
	Status         TransactionStatus
	Action         Action

	Amount   Amount
	Currency string
	OrderID  string
	Customer Customer
	// CustomID is the value the caller passed as CustomID when it opened the
	// hosted page.
	CustomID string

	PaymentMethod PaymentMethod
	PaymentInfo   PaymentInfo
	AuthCode      string
	Items         []LineItem
	PaidAt        *time.Time

	// Token is the reusable card token. It is set only on a token callback
	// that succeeded, so a failed binding can never hand one back.
	Token string

	SubscriptionID  string
	Period          int
	NumberOfPeriods int
	NextChargeAt    *time.Time

	// Message is Oen's error text when the callback reports a failure.
	Message string

	// RedactedPayload is the body with secrets removed and card numbers
	// reduced. It is safe to log or store; the raw body is not.
	RedactedPayload json.RawMessage
}

// TokenBound reports a card binding that succeeded and therefore carries a
// token.
func (e *WebhookEvent) TokenBound() bool {
	return e.Purpose == PurposeToken && e.HasOutcome && e.Success && e.Token != ""
}

// ChargeSucceeded reports a payment Oen says went through. It is still the
// provider's claim, not a verified fact; see [WebhookEvent.Verified].
func (e *WebhookEvent) ChargeSucceeded() bool {
	return e.Purpose == PurposeCharge && e.HasOutcome && e.Success
}

type wireWebhook struct {
	MerchantID      json.RawMessage   `json:"merchantId"`
	Success         json.RawMessage   `json:"success"`
	ID              json.RawMessage   `json:"id"`
	Purpose         json.RawMessage   `json:"purpose"`
	Status          json.RawMessage   `json:"status"`
	TransactionHID  json.RawMessage   `json:"transactionHid"`
	Action          json.RawMessage   `json:"action"`
	Amount          json.RawMessage   `json:"amount"`
	Currency        json.RawMessage   `json:"currency"`
	OrderID         json.RawMessage   `json:"orderId"`
	UserID          json.RawMessage   `json:"userId"`
	UserName        json.RawMessage   `json:"userName"`
	UserEmail       json.RawMessage   `json:"userEmail"`
	CustomID        json.RawMessage   `json:"customId"`
	PaymentMethod   json.RawMessage   `json:"paymentMethod"`
	PaymentInfo     json.RawMessage   `json:"paymentInfo"`
	AuthCode        json.RawMessage   `json:"authCode"`
	ProductDetails  []wireProductItem `json:"productDetails"`
	PaidAt          json.RawMessage   `json:"paidAt"`
	Token           json.RawMessage   `json:"token"`
	SubscriptionID  json.RawMessage   `json:"subscriptionId"`
	Period          json.RawMessage   `json:"period"`
	NumberOfPeriods json.RawMessage   `json:"numberOfPeriods"`
	NextChargeAt    json.RawMessage   `json:"nextChargeAt"`
	Message         json.RawMessage   `json:"message"`
	CreatedAt       json.RawMessage   `json:"createdAt"`
	OccurredAt      json.RawMessage   `json:"occurredAt"`
}

type wireProductItem struct {
	ProductionCode json.RawMessage `json:"productionCode"`
	Description    json.RawMessage `json:"description"`
	Quantity       json.RawMessage `json:"quantity"`
	Unit           json.RawMessage `json:"unit"`
	UnitPrice      json.RawMessage `json:"unitPrice"`
}

// ParseWebhook reads one Oen callback body.
//
// It performs no I/O and no authentication: it decodes the payload, checks
// that the callback names this merchant, and sanitizes the copy it returns.
// Answer Oen with [WriteAck] once the event has been recorded.
func (c *Client) ParseWebhook(raw []byte) (*WebhookEvent, error) {
	const op = "ParseWebhook"
	var wire wireWebhook
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, &Error{Op: op, Kind: KindInvalidRequest, Err: fmt.Errorf("decode webhook: %w", err)}
	}
	merchantID, err := requiredWebhookString("merchantId", wire.MerchantID)
	if err != nil {
		return nil, err
	}
	if merchantID != c.cfg.MerchantID {
		return nil, invalidWebhookField("merchantId", "callback names another merchant")
	}
	id, err := requiredWebhookString("id", wire.ID)
	if err != nil {
		return nil, err
	}
	purposeText, err := requiredWebhookString("purpose", wire.Purpose)
	if err != nil {
		return nil, err
	}
	purpose := WebhookPurpose(purposeText)
	if purpose != PurposeToken && purpose != PurposeCharge {
		return nil, invalidWebhookField("purpose", "unsupported callback purpose")
	}
	var success bool
	if isNullRaw(wire.Success) || json.Unmarshal(wire.Success, &success) != nil {
		return nil, invalidWebhookField("success", "expected a boolean")
	}
	token := ""
	if purpose == PurposeToken && success {
		token, err = requiredWebhookString("token", wire.Token)
		if err != nil {
			return nil, err
		}
	}
	redacted, err := SanitizeJSON(raw)
	if err != nil {
		return nil, &Error{Op: op, Kind: KindInvalidRequest, Err: fmt.Errorf("sanitize webhook: %w", err)}
	}

	// Decode returned fields from the sanitized copy. The successful token
	// was captured explicitly above and is the only secret intentionally returned.
	if err := json.Unmarshal(redacted, &wire); err != nil {
		return nil, invalidWebhookField("payload", "cannot decode sanitized callback")
	}

	amount, err := parseOptionalAmount(wire.Amount)
	if err != nil {
		return nil, invalidWebhookField("amount", "expected a whole number")
	}
	items, err := webhookItems(wire.ProductDetails)
	if err != nil {
		return nil, err
	}

	period, err := parseOptionalInt(wire.Period)
	if err != nil {
		return nil, invalidWebhookField("period", "expected an integer in range")
	}
	numberOfPeriods, err := parseOptionalInt(wire.NumberOfPeriods)
	if err != nil {
		return nil, invalidWebhookField("numberOfPeriods", "expected an integer in range")
	}
	loc := c.cfg.NaiveTimeLocation
	status := TransactionStatus(rawString(wire.Status))

	event := &WebhookEvent{
		MerchantID:      merchantID,
		Purpose:         purpose,
		Success:         success,
		HasOutcome:      true,
		PayloadSHA256:   sha256Hex(raw),
		ID:              id,
		TransactionHID:  rawString(wire.TransactionHID),
		Status:          status,
		Action:          Action(rawString(wire.Action)),
		Amount:          amount,
		Currency:        rawString(wire.Currency),
		OrderID:         rawString(wire.OrderID),
		Customer:        Customer{ID: rawString(wire.UserID), Name: rawString(wire.UserName), Email: rawString(wire.UserEmail)},
		CustomID:        rawString(wire.CustomID),
		PaymentMethod:   PaymentMethod(rawString(wire.PaymentMethod)),
		PaymentInfo:     decodePaymentInfo(wire.PaymentInfo, loc),
		AuthCode:        rawString(wire.AuthCode),
		Items:           items,
		PaidAt:          optionalTime(wire.PaidAt, loc),
		SubscriptionID:  rawString(wire.SubscriptionID),
		Period:          period,
		NumberOfPeriods: numberOfPeriods,
		NextChargeAt:    optionalTime(wire.NextChargeAt, loc),
		Message:         truncateRunes(rawString(wire.Message), 500),
		RedactedPayload: redacted,
	}
	if event.PaidAt == nil {
		event.PaidAt = optionalTime(wire.OccurredAt, loc)
	}
	if purpose == PurposeToken && success {
		event.Token = token
	}
	event.EventID = event.ID
	return event, nil
}

// WriteAck writes the acknowledgement Oen expects.
func WriteAck(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", AckContentType)
	w.WriteHeader(AckStatus)
	_, err := w.Write(AckBody)
	return err
}

func webhookItems(details []wireProductItem) ([]LineItem, error) {
	if len(details) == 0 {
		return nil, nil
	}
	items := make([]LineItem, len(details))
	for i, detail := range details {
		unitPrice, err := parseAmount(detail.UnitPrice)
		if err != nil {
			return nil, invalidWebhookField("productDetails.unitPrice", "expected a whole number")
		}
		quantity, err := parseOptionalInt(detail.Quantity)
		if err != nil {
			return nil, invalidWebhookField("productDetails.quantity", "expected an integer in range")
		}
		items[i] = LineItem{
			ProductionCode: rawString(detail.ProductionCode),
			Description:    rawString(detail.Description),
			Quantity:       quantity,
			Unit:           rawString(detail.Unit),
			UnitPrice:      unitPrice,
		}
	}
	return items, nil
}

func sha256Hex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func invalidWebhookField(field, message string) *Error {
	return &Error{Op: "ParseWebhook", Kind: KindInvalidRequest, Err: fmt.Errorf("%s: %s", field, message)}
}

func requiredWebhookString(field string, raw json.RawMessage) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return "", invalidWebhookField(field, "expected a nonempty string")
	}
	return strings.TrimSpace(value), nil
}
