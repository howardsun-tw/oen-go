package oen

import (
	"context"
	"encoding/json"
)

// TokenChargeRequest charges a card token once (用 Token 發起單筆交易).
//
// Set IdempotencyKey on the first attempt to enable optional resend protection.
// The SDK never retries automatically.
type TokenChargeRequest struct {
	// IdempotencyKey optionally protects resends of the same request within 24 hours.
	// Set it on the first attempt. See the package documentation for limitations.
	IdempotencyKey string
	// OrderID is the merchant's own order reference. Reusing one is what makes
	// [Client.ListOrderTransactions] able to resolve an uncertain charge.
	OrderID string
	// Token is the reusable card token from a token webhook.
	Token  string
	Amount Amount
	// Currency defaults to [CurrencyTWD].
	Currency string
	// Items must add up to Amount.
	Items    []LineItem
	Customer Customer
	// InvoiceInfo asks Oen to issue a specific kind of invoice. Leave it nil
	// to keep Oen's default behaviour.
	InvoiceInfo *InvoiceInfo
	Note        string
	// ExpectedPayoutDate is advisory and formatted yyyy/MM/dd in UTC+8.
	ExpectedPayoutDate string
}

// TokenSubscriptionRequest starts a recurring charge on a card token
// (用 Token 發起定期定額交易). Oen then charges the following periods itself.
type TokenSubscriptionRequest struct {
	// IdempotencyKey optionally protects resends of the same request within 24 hours.
	// Set it on the first attempt. See the package documentation for limitations.
	IdempotencyKey string
	OrderID        string
	Token          string
	Amount         Amount
	Currency       string
	Items          []LineItem
	Customer       Customer

	InvoiceInfo *InvoiceInfo
	// NumberOfPeriods is the total number of charges; zero means open-ended.
	NumberOfPeriods int
	// PaymentInterval is the gap between charges in months, 1 to 12. Zero
	// leaves Oen's default of one month.
	PaymentInterval int
	// StartDate is the first charge date, yyyy/MM/dd in UTC+8. Leave it empty
	// to charge immediately.
	StartDate string
	Note      string
}

// ChargeResult is Oen's answer to a token charge.
//
// An error instead of a result does not always mean the money stayed put:
// when the error satisfies [ErrUnknownOutcome], call
// [Client.ListOrderTransactions] with the same OrderID before deciding
// anything. A protected resend must use the original key and identical request
// within 24 hours; I0003 and C026 require querying, not another charge.
type ChargeResult struct {
	// TransactionHID is Oen's transaction identifier, the one refunds take.
	TransactionHID string
	AuthCode       string
	// RedactedPayload is the response with secrets removed.
	RedactedPayload json.RawMessage
}

// SubscriptionResult is Oen's answer to a token subscription.
type SubscriptionResult struct {
	SubscriptionID string
	// TransactionHID is the first charge. Oen names this field transactionId
	// in its response, but the value is a transaction HID.
	TransactionHID  string
	AuthCode        string
	RedactedPayload json.RawMessage
}

// ChargeToken charges a bound card once. The request is sent exactly once.
func (c *Client) ChargeToken(ctx context.Context, req TokenChargeRequest) (*ChargeResult, error) {
	const op = "ChargeToken"
	payload, err := c.tokenPayload(op, req.OrderID, req.Token, req.Amount, req.Currency, req.Items, req.Customer)
	if err != nil {
		return nil, err
	}
	if err := validateProviderDate(op, "expectedPayoutDate", req.ExpectedPayoutDate); err != nil {
		return nil, err
	}
	addOptional(payload, "note", req.Note)
	addOptional(payload, "expectedPayoutDate", req.ExpectedPayoutDate)
	addInvoiceInfo(payload, req.InvoiceInfo)

	resp, err := c.post(ctx, op, "/token/transactions", payload, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	var data struct {
		ID       json.RawMessage `json:"id"`
		AuthCode json.RawMessage `json:"authCode"`
	}
	if err := decodeData(op, resp, &data); err != nil {
		return nil, err
	}
	id := rawString(data.ID)
	if id == "" {
		return nil, unknownResponseOutcome(op, resp, errNoField("data.id"))
	}
	return &ChargeResult{
		TransactionHID:  id,
		AuthCode:        rawString(data.AuthCode),
		RedactedPayload: redactedResource(resp.body),
	}, nil
}

// SubscribeToken starts a recurring charge on a bound card. The request is
// sent exactly once; an [ErrUnknownOutcome] answer may already have created
// the subscription, so query before starting another.
func (c *Client) SubscribeToken(ctx context.Context, req TokenSubscriptionRequest) (*SubscriptionResult, error) {
	const op = "SubscribeToken"
	payload, err := c.tokenPayload(op, req.OrderID, req.Token, req.Amount, req.Currency, req.Items, req.Customer)
	if err != nil {
		return nil, err
	}
	if err := validateSchedule(op, req.NumberOfPeriods, req.PaymentInterval, req.StartDate); err != nil {
		return nil, err
	}
	addOptional(payload, "note", req.Note)
	addOptional(payload, "startDate", req.StartDate)
	addInvoiceInfo(payload, req.InvoiceInfo)
	if req.NumberOfPeriods > 0 {
		payload["numberOfPeriods"] = req.NumberOfPeriods
	}
	if req.PaymentInterval > 0 {
		payload["paymentInterval"] = req.PaymentInterval
	}

	resp, err := c.post(ctx, op, "/token/subscriptions", payload, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	var data struct {
		SubscriptionID json.RawMessage `json:"subscriptionId"`
		TransactionID  json.RawMessage `json:"transactionId"`
		AuthCode       json.RawMessage `json:"authCode"`
	}
	if err := decodeData(op, resp, &data); err != nil {
		return nil, err
	}
	subscriptionID := rawString(data.SubscriptionID)
	if subscriptionID == "" {
		return nil, unknownResponseOutcome(op, resp, errNoField("data.subscriptionId"))
	}
	return &SubscriptionResult{
		SubscriptionID:  subscriptionID,
		TransactionHID:  rawString(data.TransactionID),
		AuthCode:        rawString(data.AuthCode),
		RedactedPayload: redactedResource(resp.body),
	}, nil
}

func (c *Client) tokenPayload(
	op, orderID, token string,
	amount Amount,
	currency string,
	items []LineItem,
	customer Customer,
) (map[string]any, error) {
	if token == "" {
		return nil, newValidationError(op, "token", "card token is required")
	}
	payload, err := c.purchasePayload(op, orderID, amount, currency, items, customer)
	if err != nil {
		return nil, err
	}
	payload["token"] = token
	return payload, nil
}

func addInvoiceInfo(payload map[string]any, info *InvoiceInfo) {
	if info == nil {
		return
	}
	payload["invoiceInfo"] = wireInvoiceInfo{
		InvoiceType:     info.InvoiceType,
		CarrierType:     info.CarrierType,
		CarrierID:       info.CarrierID,
		BuyerIdentifier: info.BuyerIdentifier,
		BuyerName:       info.BuyerName,
		Email:           info.Email,
	}
}
