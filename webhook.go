package oen

import (
	"bytes"
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
	"unicode/utf16"
	"unicode/utf8"
)

// The acknowledgement Oen expects. Oen retries a failed webhook three times,
// two, four and six seconds apart, so an endpoint that cannot answer 200
// quickly will be called again.
var (
	AckStatus      = http.StatusOK
	AckContentType = "application/json"
	AckBody        = []byte(`{"received":true}`)
)

// Webhook headers Oen sends with every payment notification. HeaderSignature
// is present only once a signing secret exists in the CRM. HeaderEventID and
// HeaderDeliveryAttempt are not covered by the signature.
const (
	HeaderSignature       = "OenPay-Signature"
	HeaderEventID         = "OenPay-Event-Id"
	HeaderDeliveryAttempt = "OenPay-Delivery-Attempt"
)

// DefaultWebhookTolerance is the default for [Config.WebhookTolerance]. Oen
// sets no limit and recommends rejecting notifications older than 5 minutes.
const DefaultWebhookTolerance = 5 * time.Minute

const webhookSecretPrefix = "pay_whsec_"

// WebhookPurpose is why Oen sent the callback. Oen may add purposes without
// notice and asks receivers to skip, not reject, ones they do not handle.
type WebhookPurpose string

const (
	// PurposeCharge is a payment result, one per instalment for
	// subscriptions. A cvs, atm or LINE Pay payment first arrives as status
	// charging without an outcome, then again with one.
	PurposeCharge WebhookPurpose = "charge"
	// PurposeToken is the result of a card binding started by
	// [Client.CreateTokenCheckout].
	PurposeToken WebhookPurpose = "token"
	// PurposeScheduleSubscription is the result of the checkout page of a
	// subscription scheduled for a later start. Each instalment then arrives
	// as a charge.
	PurposeScheduleSubscription WebhookPurpose = "schedule_subscription"
	// PurposeSubscriptionCardChanged reports a successful card change on the
	// change-card page.
	PurposeSubscriptionCardChanged WebhookPurpose = "subscription_card_changed"

	// The purposes below are sent only after Oen enables "Webhook 新版事件"
	// for the merchant; all five are enabled together.

	// PurposeRefund reports a refund created, completed or cancelled.
	PurposeRefund WebhookPurpose = "refund"
	// PurposeSubscriptionCancelled reports a subscription cancelled through
	// the API or the CRM.
	PurposeSubscriptionCancelled WebhookPurpose = "subscription_cancelled"
	// PurposeSubscriptionRetry reports a failed instalment that was scheduled
	// for another attempt, or that used up its attempts.
	PurposeSubscriptionRetry WebhookPurpose = "subscription_retry"
	// PurposeInvoiceIssued reports an e-invoice issued for an API transaction.
	PurposeInvoiceIssued WebhookPurpose = "invoice_issued"
	// PurposeInvoiceVoided reports an e-invoice voided for an API transaction.
	PurposeInvoiceVoided WebhookPurpose = "invoice_voided"
)

var invoiceRequiredFields = []string{
	"merchantId", "id", "chargeId", "invoiceNumber", "invoiceStatus",
	"invoiceDate", "randomNumber", "invoiceAmount", "createdAt",
}

// Known reports whether this SDK version documents the purpose. An unknown
// purpose still parses: only the identity fields, Status, OrderID, CustomID,
// Message and RedactedPayload are filled, and no other field can make it
// fail. Skip it and acknowledge it.
func (p WebhookPurpose) Known() bool {
	_, ok := webhookRequiredFields[p]
	return ok
}

// webhookRequiredFields lists, per purpose, the fields Oen documents as always
// present (一定有). "success" must be a boolean and "invoiceAmount" must be
// present but may be null; the others must be nonempty strings. Oen's refund,
// subscription_cancelled and subscription_retry tables list only
// purpose-specific fields; their id is the documented event key, but
// merchantId is not listed and so is checked only when present. Fields that
// repeat another (transactionId) are not required. Charge status and
// transactionHid are marked 一定有 but were never required by this SDK.
var webhookRequiredFields = map[WebhookPurpose][]string{
	PurposeCharge:                  {"merchantId", "id"},
	PurposeToken:                   {"merchantId", "id", "success"},
	PurposeScheduleSubscription:    {"merchantId", "id", "success", "subscriptionId"},
	PurposeSubscriptionCardChanged: {"merchantId", "id", "success", "status", "subscriptionId"},
	PurposeRefund:                  {"id"},
	PurposeSubscriptionCancelled:   {"id"},
	PurposeSubscriptionRetry:       {"id"},
	PurposeInvoiceIssued:           invoiceRequiredFields,
	PurposeInvoiceVoided:           invoiceRequiredFields,
}

// WebhookEvent is one parsed Oen callback.
//
// Parsing is not authentication: an event from [Client.ParseWebhook] has
// Verified false and may be forged. An event from [Client.VerifyWebhook] is
// authentic but may be a duplicate or out of order, so it is still not the
// latest state. Confirm anything that matters with [Client.GetTransaction] or
// [Client.ListOrderTransactions] before changing state.
type WebhookEvent struct {
	// Verified reports that [Client.VerifyWebhook] matched the
	// OenPay-Signature header against the body.
	Verified bool
	// DeliveryAttempt is the OenPay-Delivery-Attempt header (1 to 3), set by
	// [Client.VerifyWebhook]. It is 0 when absent or after ParseWebhook.
	DeliveryAttempt int

	MerchantID string
	Purpose    WebhookPurpose

	// Success is Oen's outcome flag. Read it together with HasOutcome.
	Success bool
	// HasOutcome reports that the body carried success. It is always true for
	// token, schedule_subscription and subscription_card_changed. A charge
	// that is still in progress (status charging) has none.
	HasOutcome bool

	// EventID (evt_…) is the deduplication key Oen documents, recomputed
	// from the body: a resend keeps it, while a later outcome for the same
	// transaction, such as charging then charged, gets a new one. It is the
	// value Oen sends in the OenPay-Event-Id header.
	EventID string
	// PayloadSHA256 is the digest of the raw body, for audit trails.
	PayloadSHA256 string

	// ID is Oen's id field: the transaction ID on a charge callback, the
	// binding session ID on a token callback, and the ID returned when the
	// page was created on a schedule_subscription callback. On the other
	// purposes it is an event key, such as refund#… or invoice#…#issued, and
	// never a transaction ID; use ChargeID for that.
	ID             string
	TransactionHID string
	// Status is the transaction status on a charge callback. On
	// subscription_card_changed it is always "succeeded", which is not a
	// TransactionStatus and so reports Known false.
	Status TransactionStatus
	Action Action

	// Amount is the callback's amount. Read it together with HasAmount: a
	// token callback carries none, and a charge callback that omits it must
	// be confirmed with [Client.GetTransaction] before the value is trusted.
	Amount Amount
	// HasAmount distinguishes an omitted amount from zero.
	HasAmount bool
	Currency  string
	OrderID   string
	Customer  Customer
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

	// ChargeID and ChargeHID (chargeId, chargeHumanId) name the transaction
	// behind a refund, subscription_retry or invoice callback. Query it with
	// [Client.GetTransaction] using ChargeID.
	ChargeID  string
	ChargeHID string

	// IsRetry, FailureCode and ScheduleStatus describe a subscription
	// instalment on a charge callback. FailureCode also appears on
	// subscription_retry.
	IsRetry        bool
	FailureCode    string
	ScheduleStatus string
	// Interval (months between charges) and StartedAt appear on
	// schedule_subscription.
	Interval  int
	StartedAt *time.Time
	// CardBrand is the new card's brand on subscription_card_changed.
	CardBrand string
	// CreatedAt is the transaction's creation time on a charge callback, but
	// the notification's creation time on an invoice callback.
	CreatedAt *time.Time
	// Reason is the refund or cancellation reason.
	Reason string

	// Refund, Cancellation, Retry and Invoice are set only for the purpose
	// they describe.
	Refund       *WebhookRefund
	Cancellation *WebhookCancellation
	Retry        *WebhookRetry
	Invoice      *WebhookInvoice

	// Message is Oen's error text when the callback reports a failure.
	Message string

	// RedactedPayload is the body with secrets removed and card numbers
	// reduced. It is safe to log or store; the raw body is not.
	RedactedPayload json.RawMessage
}

// WebhookRefund is the refund-specific part of a refund callback. The refund
// amount is [WebhookEvent.Amount].
type WebhookRefund struct {
	RefundID string
	// Status is refunding, refunded or cancelled.
	Status string
	// OriginalAmount is the original transaction's amount, zero when absent.
	OriginalAmount Amount
	RequestedAt    *time.Time
	ResolvedAt     *time.Time
}

// WebhookCancellation is the cancellation-specific part of a
// subscription_cancelled callback. The reason is [WebhookEvent.Reason].
type WebhookCancellation struct {
	// Source is merchant_api or crm.
	Source              string
	CancelledAt         *time.Time
	CancelledFromStatus string
}

// WebhookRetry is the retry-specific part of a subscription_retry callback.
type WebhookRetry struct {
	// Outcome is scheduled (another attempt is planned) or exhausted (the
	// subscription stopped).
	Outcome       string
	AttemptNumber int
	// NextRetryAt is set only when Outcome is scheduled.
	NextRetryAt *time.Time
}

// WebhookInvoice is the e-invoice on an invoice_issued or invoice_voided
// callback; on invoice_voided it is the voided invoice.
type WebhookInvoice struct {
	Number string
	// Status is issued or voided, as invoice.invoiceStatus on a query.
	Status       string
	Date         *time.Time
	RandomNumber string
	// Amount is the total including tax. Oen sends null when the invoice
	// items are incomplete, which leaves HasAmount false.
	Amount    Amount
	HasAmount bool
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
	SubscriptionID  json.RawMessage   `json:"subscriptionId"`
	Period          json.RawMessage   `json:"period"`
	NumberOfPeriods json.RawMessage   `json:"numberOfPeriods"`
	NextChargeAt    json.RawMessage   `json:"nextChargeAt"`
	Message         json.RawMessage   `json:"message"`
	CreatedAt       json.RawMessage   `json:"createdAt"`
	OccurredAt      json.RawMessage   `json:"occurredAt"`

	ChargeID            json.RawMessage `json:"chargeId"`
	ChargeHID           json.RawMessage `json:"chargeHumanId"`
	IsRetry             json.RawMessage `json:"isRetry"`
	FailureCode         json.RawMessage `json:"failureCode"`
	ScheduleStatus      json.RawMessage `json:"scheduleStatus"`
	Interval            json.RawMessage `json:"interval"`
	StartedAt           json.RawMessage `json:"startedAt"`
	CardBrand           json.RawMessage `json:"cardBrand"`
	Reason              json.RawMessage `json:"reason"`
	RefundID            json.RawMessage `json:"refundId"`
	RefundStatus        json.RawMessage `json:"refundStatus"`
	OrigAmount          json.RawMessage `json:"origAmount"`
	RequestedAt         json.RawMessage `json:"requestedAt"`
	ResolvedAt          json.RawMessage `json:"resolvedAt"`
	Source              json.RawMessage `json:"source"`
	CancelledAt         json.RawMessage `json:"cancelledAt"`
	CancelledFromStatus json.RawMessage `json:"cancelledFromStatus"`
	Outcome             json.RawMessage `json:"outcome"`
	RetryAttemptNumber  json.RawMessage `json:"retryAttemptNumber"`
	NextRetryAt         json.RawMessage `json:"nextRetryAt"`
	InvoiceNumber       json.RawMessage `json:"invoiceNumber"`
	InvoiceStatus       json.RawMessage `json:"invoiceStatus"`
	InvoiceDate         json.RawMessage `json:"invoiceDate"`
	RandomNumber        json.RawMessage `json:"randomNumber"`
	InvoiceAmount       json.RawMessage `json:"invoiceAmount"`
}

type wireProductItem struct {
	ProductionCode json.RawMessage `json:"productionCode"`
	Description    json.RawMessage `json:"description"`
	Quantity       json.RawMessage `json:"quantity"`
	Unit           json.RawMessage `json:"unit"`
	UnitPrice      json.RawMessage `json:"unitPrice"`
}

// VerifyWebhook authenticates one payment notification and parses it. Pass
// the request body exactly as received; re-encoded JSON will not match.
//
// It requires [Config.WebhookSecret] and returns [ErrInvalidInput] without
// one. A missing, malformed, stale or non-matching OenPay-Signature, or an
// OenPay-Event-Id header that differs from the event ID recomputed from the
// body, returns [ErrInvalidSignature]: answer with a 4xx status. After the
// signature matches, the body is read as by [Client.ParseWebhook], whose
// errors are returned unchanged.
func (c *Client) VerifyWebhook(raw []byte, header http.Header) (*WebhookEvent, error) {
	if err := c.VerifyWebhookSignature(raw, header.Get(HeaderSignature)); err != nil {
		return nil, err
	}
	event, err := c.ParseWebhook(raw)
	if err != nil {
		return nil, err
	}
	if sent := strings.TrimSpace(header.Get(HeaderEventID)); sent != "" && sent != event.EventID {
		return nil, &Error{Op: "VerifyWebhook", Kind: KindInvalidSignature, Err: fmt.Errorf("%s header does not match the body", HeaderEventID)}
	}
	if attempt, err := strconv.Atoi(strings.TrimSpace(header.Get(HeaderDeliveryAttempt))); err == nil && attempt > 0 {
		event.DeliveryAttempt = attempt
	}
	event.Verified = true
	return event, nil
}

// VerifyWebhookSignature checks one OenPay-Signature value,
// "t=<unix seconds>,v1=<hex>", against the raw body. The signed message is
// "<t>.<body>" with HMAC-SHA256 under [Config.WebhookSecret], and t must be
// within [Config.WebhookTolerance] of [Config.Now]. It does not read the
// body, so it also authenticates a body ParseWebhook cannot read.
//
// Unlike the Subscription API, the Payment API keeps no old secret after a
// rotation. Any matching v1 value is accepted.
func (c *Client) VerifyWebhookSignature(raw []byte, signature string) error {
	const op = "VerifyWebhookSignature"
	// An empty key would accept a signature anyone can compute.
	if c.cfg.WebhookSecret == "" {
		return newValidationError(op, "webhookSecret", "webhook secret is not configured")
	}
	invalid := func(message string) error {
		return &Error{Op: op, Kind: KindInvalidSignature, Err: errors.New(message)}
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
				return invalid("more than one timestamp")
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
		return invalid("missing or malformed timestamp")
	}
	if len(signatures) == 0 {
		return invalid("no v1 signature")
	}
	tolerance := c.cfg.WebhookTolerance
	age := c.cfg.Now().Sub(time.Unix(seconds, 0))
	if age > tolerance || age < -tolerance {
		return invalid(fmt.Sprintf("timestamp is outside the %s tolerance", tolerance))
	}

	mac := hmac.New(sha256.New, []byte(c.cfg.WebhookSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(raw)
	expected := mac.Sum(nil)
	for _, candidate := range signatures {
		if hmac.Equal(candidate, expected) {
			return nil
		}
	}
	return invalid("no signature matches the body")
}

// ParseWebhook reads one Oen callback body.
//
// It performs no I/O and no authentication: it decodes the payload, checks
// that the callback names this merchant, and sanitizes the copy it returns.
// Use [Client.VerifyWebhook] instead when a signing secret exists.
// Answer Oen with [WriteAck] once the event has been recorded.
func (c *Client) ParseWebhook(raw []byte) (*WebhookEvent, error) {
	const op = "ParseWebhook"
	// The identity fields are read by exact key, as JavaScript does; struct
	// decoding would also accept "ID" or "Purpose" and break the event ID.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, &Error{Op: op, Kind: KindInvalidRequest, Err: fmt.Errorf("decode webhook: %w", err)}
	}
	purposeText, err := requiredWebhookString("purpose", fields["purpose"])
	if err != nil {
		return nil, err
	}
	purpose := WebhookPurpose(purposeText)
	merchantID := ""
	if !isNullRaw(fields["merchantId"]) {
		if merchantID, err = requiredWebhookString("merchantId", fields["merchantId"]); err != nil {
			return nil, err
		}
		if merchantID != c.cfg.MerchantID {
			return nil, invalidWebhookField("merchantId", "callback names another merchant")
		}
	}
	for _, field := range webhookRequiredFields[purpose] {
		switch field {
		case "success":
			if isNullRaw(fields["success"]) {
				return nil, invalidWebhookField("success", "expected a boolean")
			}
		case "invoiceAmount":
			if _, present := fields[field]; !present {
				return nil, invalidWebhookField(field, "expected a number or null")
			}
		default:
			if _, err := requiredWebhookString(field, fields[field]); err != nil {
				return nil, err
			}
		}
	}
	var success bool
	hasOutcome := !isNullRaw(fields["success"]) && json.Unmarshal(fields["success"], &success) == nil
	if !hasOutcome && !isNullRaw(fields["success"]) && purpose.Known() {
		return nil, invalidWebhookField("success", "expected a boolean")
	}
	token := ""
	if purpose == PurposeToken && success {
		token, err = requiredWebhookString("token", fields["token"])
		if err != nil {
			return nil, err
		}
	}
	eventID, err := webhookEventID(fields)
	if err != nil {
		return nil, err
	}
	redacted, err := SanitizeJSON(raw)
	if err != nil {
		return nil, &Error{Op: op, Kind: KindInvalidRequest, Err: fmt.Errorf("sanitize webhook: %w", err)}
	}

	// Every returned field is read from the sanitized copy, decoded into a
	// fresh struct so nothing from the raw pass can survive. The successful
	// token was captured above and is the only secret intentionally returned.
	var clean wireWebhook
	if err := json.Unmarshal(redacted, &clean); err != nil {
		return nil, invalidWebhookField("payload", "cannot decode sanitized callback")
	}
	event := &WebhookEvent{
		MerchantID:      merchantID,
		Purpose:         purpose,
		Success:         success,
		HasOutcome:      hasOutcome,
		EventID:         eventID,
		PayloadSHA256:   sha256Hex(raw),
		ID:              rawString(fields["id"]),
		Status:          TransactionStatus(rawString(fields["status"])),
		OrderID:         rawString(clean.OrderID),
		CustomID:        rawString(clean.CustomID),
		Message:         rawString(clean.Message),
		RedactedPayload: redacted,
	}
	if !purpose.Known() {
		return event, nil
	}

	var isRetry bool
	if !isNullRaw(clean.IsRetry) && json.Unmarshal(clean.IsRetry, &isRetry) != nil {
		return nil, invalidWebhookField("isRetry", "expected a boolean")
	}
	amount, err := parseOptionalAmount(clean.Amount)
	if err != nil {
		return nil, invalidWebhookField("amount", "expected a whole number")
	}
	items, err := webhookItems(clean.ProductDetails)
	if err != nil {
		return nil, err
	}

	period, err := parseOptionalInt(clean.Period)
	if err != nil {
		return nil, invalidWebhookField("period", "expected an integer in range")
	}
	numberOfPeriods, err := parseOptionalInt(clean.NumberOfPeriods)
	if err != nil {
		return nil, invalidWebhookField("numberOfPeriods", "expected an integer in range")
	}
	loc := c.cfg.NaiveTimeLocation
	paymentInfo, err := decodePaymentInfo(clean.PaymentInfo, loc)
	if err != nil {
		return nil, invalidWebhookField("paymentInfo", err.Error())
	}
	paidAt, err := optionalTime(clean.PaidAt, loc)
	if err != nil {
		return nil, invalidWebhookField("paidAt", err.Error())
	}
	if paidAt == nil {
		if paidAt, err = optionalTime(clean.OccurredAt, loc); err != nil {
			return nil, invalidWebhookField("occurredAt", err.Error())
		}
	}
	interval, err := parseOptionalInt(clean.Interval)
	if err != nil {
		return nil, invalidWebhookField("interval", "expected an integer in range")
	}
	times := map[string]*time.Time{}
	for field, value := range map[string]json.RawMessage{
		"nextChargeAt": clean.NextChargeAt,
		"startedAt":    clean.StartedAt,
		"createdAt":    clean.CreatedAt,
	} {
		if times[field], err = optionalTime(value, loc); err != nil {
			return nil, invalidWebhookField(field, err.Error())
		}
	}

	event.TransactionHID = rawString(clean.TransactionHID)
	event.Action = Action(rawString(clean.Action))
	event.Amount = amount
	event.HasAmount = !isNullRaw(clean.Amount)
	event.Currency = rawString(clean.Currency)
	event.Customer = Customer{ID: rawString(clean.UserID), Name: rawString(clean.UserName), Email: rawString(clean.UserEmail)}
	event.PaymentMethod = PaymentMethod(rawString(clean.PaymentMethod))
	event.PaymentInfo = paymentInfo
	event.AuthCode = rawString(clean.AuthCode)
	event.Items = items
	event.PaidAt = paidAt
	event.Token = token
	event.SubscriptionID = rawString(clean.SubscriptionID)
	event.Period = period
	event.NumberOfPeriods = numberOfPeriods
	event.NextChargeAt = times["nextChargeAt"]
	event.ChargeID = rawString(clean.ChargeID)
	event.ChargeHID = rawString(clean.ChargeHID)
	event.IsRetry = isRetry
	event.FailureCode = rawString(clean.FailureCode)
	event.ScheduleStatus = rawString(clean.ScheduleStatus)
	event.Interval = interval
	event.StartedAt = times["startedAt"]
	event.CardBrand = rawString(clean.CardBrand)
	event.CreatedAt = times["createdAt"]
	event.Reason = rawString(clean.Reason)
	if err := c.parseWebhookDetail(event, clean); err != nil {
		return nil, err
	}
	return event, nil
}

// parseWebhookDetail fills the struct that belongs to the event's purpose.
func (c *Client) parseWebhookDetail(event *WebhookEvent, clean wireWebhook) error {
	loc := c.cfg.NaiveTimeLocation
	timeField := func(field string, raw json.RawMessage) (*time.Time, error) {
		parsed, err := optionalTime(raw, loc)
		if err != nil {
			return nil, invalidWebhookField(field, err.Error())
		}
		return parsed, nil
	}
	var err error
	switch event.Purpose {
	case PurposeRefund:
		refund := &WebhookRefund{RefundID: rawString(clean.RefundID), Status: rawString(clean.RefundStatus)}
		if refund.OriginalAmount, err = parseOptionalAmount(clean.OrigAmount); err != nil {
			return invalidWebhookField("origAmount", "expected a whole number")
		}
		if refund.RequestedAt, err = timeField("requestedAt", clean.RequestedAt); err != nil {
			return err
		}
		if refund.ResolvedAt, err = timeField("resolvedAt", clean.ResolvedAt); err != nil {
			return err
		}
		event.Refund = refund
	case PurposeSubscriptionCancelled:
		cancellation := &WebhookCancellation{Source: rawString(clean.Source), CancelledFromStatus: rawString(clean.CancelledFromStatus)}
		if cancellation.CancelledAt, err = timeField("cancelledAt", clean.CancelledAt); err != nil {
			return err
		}
		event.Cancellation = cancellation
	case PurposeSubscriptionRetry:
		retry := &WebhookRetry{Outcome: rawString(clean.Outcome)}
		if retry.AttemptNumber, err = parseOptionalInt(clean.RetryAttemptNumber); err != nil {
			return invalidWebhookField("retryAttemptNumber", "expected an integer in range")
		}
		if retry.NextRetryAt, err = timeField("nextRetryAt", clean.NextRetryAt); err != nil {
			return err
		}
		event.Retry = retry
	case PurposeInvoiceIssued, PurposeInvoiceVoided:
		invoice := &WebhookInvoice{
			Number:       rawString(clean.InvoiceNumber),
			Status:       rawString(clean.InvoiceStatus),
			RandomNumber: rawString(clean.RandomNumber),
			HasAmount:    !isNullRaw(clean.InvoiceAmount),
		}
		if invoice.Amount, err = parseOptionalAmount(clean.InvoiceAmount); err != nil {
			return invalidWebhookField("invoiceAmount", "expected a whole number")
		}
		if invoice.Date, err = timeField("invoiceDate", clean.InvoiceDate); err != nil {
			return err
		}
		event.Invoice = invoice
	}
	return nil
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

// webhookEventID follows Oen's documented OenPay-Event-Id algorithm: "evt_"
// and the first 32 hex digits of the SHA-256 of JavaScript's
// JSON.stringify([purpose, merchantId, id, status, success]), with null for
// an absent field. Oen's own receivers apply it to every purpose. The values
// are the untrimmed body values. Only strings, booleans and null are
// accepted, because Go cannot reproduce JavaScript's number formatting.
func webhookEventID(fields map[string]json.RawMessage) (string, error) {
	identity := []byte{'['}
	for i, field := range []string{"purpose", "merchantId", "id", "status", "success"} {
		if i > 0 {
			identity = append(identity, ',')
		}
		token := bytes.TrimSpace(fields[field])
		switch {
		case isNullRaw(token):
			identity = append(identity, "null"...)
		case string(token) == "true" || string(token) == "false":
			identity = append(identity, token...)
		case token[0] == '"':
			identity = appendJSStringToken(identity, token)
		default:
			return "", invalidWebhookField(field, "expected a string, boolean or null")
		}
	}
	identity = append(identity, ']')
	return "evt_" + sha256Hex(identity)[:32], nil
}

// appendJSStringToken re-encodes a valid JSON string literal as
// JSON.stringify(JSON.parse(token)) does. It works on UTF-16 code units, as
// JavaScript does: encoding/json would turn a lone surrogate escape such as
// \ud800 into U+FFFD, while JavaScript keeps and re-escapes it. Raw invalid
// UTF-8 becomes one U+FFFD per byte, which can differ from how a JavaScript
// receiver decodes the request body.
func appendJSStringToken(dst, token []byte) []byte {
	var units []uint16
	for i := 1; i < len(token)-1; {
		if token[i] != '\\' {
			r, size := utf8.DecodeRune(token[i:])
			units = utf16.AppendRune(units, r)
			i += size
			continue
		}
		escape := token[i+1]
		i += 2
		switch escape {
		case 'u':
			unit, _ := strconv.ParseUint(string(token[i:i+4]), 16, 16)
			units = append(units, uint16(unit))
			i += 4
		case 'b':
			units = append(units, '\b')
		case 'f':
			units = append(units, '\f')
		case 'n':
			units = append(units, '\n')
		case 'r':
			units = append(units, '\r')
		case 't':
			units = append(units, '\t')
		default: // '"', '\\' and '/' stand for themselves.
			units = append(units, uint16(escape))
		}
	}

	const hexDigits = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(units); i++ {
		unit := rune(units[i])
		if utf16.IsSurrogate(unit) {
			if unit < 0xdc00 && i+1 < len(units) {
				if pair := utf16.DecodeRune(unit, rune(units[i+1])); pair != utf8.RuneError {
					dst = utf8.AppendRune(dst, pair)
					i++
					continue
				}
			}
			dst = append(dst, '\\', 'u', hexDigits[unit>>12], hexDigits[unit>>8&0xf], hexDigits[unit>>4&0xf], hexDigits[unit&0xf])
			continue
		}
		switch {
		case unit == '"':
			dst = append(dst, '\\', '"')
		case unit == '\\':
			dst = append(dst, '\\', '\\')
		case unit == '\b':
			dst = append(dst, '\\', 'b')
		case unit == '\f':
			dst = append(dst, '\\', 'f')
		case unit == '\n':
			dst = append(dst, '\\', 'n')
		case unit == '\r':
			dst = append(dst, '\\', 'r')
		case unit == '\t':
			dst = append(dst, '\\', 't')
		case unit < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[unit>>4], hexDigits[unit&0xf])
		default:
			dst = utf8.AppendRune(dst, unit)
		}
	}
	return append(dst, '"')
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
