package oen

import (
	"context"
	"encoding/json"
)

// CheckoutRequest opens Oen's hosted page for one payment (取得單次交易頁面).
type CheckoutRequest struct {
	// OrderID is the merchant's own order reference (orderId).
	OrderID string
	Amount  Amount
	// Currency defaults to [CurrencyTWD], the only value Oen documents.
	Currency string
	// Items must add up to Amount; Oen refuses the request otherwise.
	Items    []LineItem
	Customer Customer

	// SuccessURL and FailureURL default to the values in [Config].
	SuccessURL string
	FailureURL string

	// AllowedPaymentMethods widens the hosted page beyond card payment.
	AllowedPaymentMethods []PaymentMethod
	// Use3D asks for 3-D Secure. Oen defaults it to false.
	Use3D bool
	// CustomID is echoed back on the webhook.
	CustomID string
	Note     string
	// ExpectedPayoutDate is advisory and formatted yyyy/MM/dd in UTC+8.
	ExpectedPayoutDate string
}

// SubscriptionCheckoutRequest opens Oen's hosted page for a recurring payment
// (取得定期定額交易頁面). Oen charges the first period immediately and then
// charges monthly on the same day.
type SubscriptionCheckoutRequest struct {
	OrderID  string
	Amount   Amount
	Currency string
	Items    []LineItem
	Customer Customer

	SuccessURL string
	FailureURL string

	Use3D bool
	// NumberOfPeriods is the total number of charges. Leave it zero for an
	// open-ended subscription; Oen then receives no numberOfPeriods field.
	NumberOfPeriods int
	CustomID        string
	Note            string
}

// ScheduleCheckoutRequest opens Oen's hosted page for a recurring payment with
// a schedule (取得設定未來交易之定期定額交易頁面).
type ScheduleCheckoutRequest struct {
	OrderID  string
	Amount   Amount
	Currency string
	Items    []LineItem
	Customer Customer

	SuccessURL string
	FailureURL string

	Use3D bool
	// NumberOfPeriods is the total number of charges; zero means open-ended.
	NumberOfPeriods int
	// PaymentInterval is the gap between charges in months, 1 to 12. Zero
	// leaves Oen's default of one month.
	PaymentInterval int
	// StartDate is the first charge date, formatted yyyy/MM/dd in UTC+8.
	// Leave it empty to charge immediately.
	StartDate string
	CustomID  string
	Note      string
}

// TokenCheckoutRequest opens Oen's hosted 3-D Secure page that turns a card
// into a reusable token (用卡號透過 3D 驗證取得 token).
//
// The token is not in the response. Oen delivers it later on the webhook, and
// [Client.ParseWebhook] reads it from there.
type TokenCheckoutRequest struct {
	SuccessURL string
	FailureURL string
	// CustomID is echoed back on the token webhook, and is how a caller ties
	// the callback to the card binding it started.
	CustomID string
	Note     string
}

// CheckoutSession is a hosted page waiting for the payer.
type CheckoutSession struct {
	// ID is the session identifier that appears in RedirectURL.
	ID string
	// TransactionHID is the transaction Oen pre-created for the session.
	TransactionHID string
	// RedirectURL is where the payer must be sent. It is empty when no
	// checkout host is configured.
	RedirectURL string
}

// CreateCheckout opens a hosted page for one payment. Sending the payer to
// [CheckoutSession.RedirectURL] is the caller's job.
func (c *Client) CreateCheckout(ctx context.Context, req CheckoutRequest) (*CheckoutSession, error) {
	const op = "CreateCheckout"
	currency, err := c.validatePurchase(op, req.OrderID, req.Amount, req.Currency, req.Items)
	if err != nil {
		return nil, err
	}
	successURL, failureURL, err := c.returnURLs(op, req.SuccessURL, req.FailureURL)
	if err != nil {
		return nil, err
	}
	if err := validateProviderDate(op, "expectedPayoutDate", req.ExpectedPayoutDate); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"merchantId":     c.cfg.MerchantID,
		"amount":         int64(req.Amount),
		"currency":       currency,
		"orderId":        req.OrderID,
		"successUrl":     successURL,
		"failureUrl":     failureURL,
		"productDetails": wireLineItems(req.Items),
	}
	addCustomer(payload, req.Customer)
	addOptional(payload, "note", req.Note)
	addOptional(payload, "customId", req.CustomID)
	addOptional(payload, "expectedPayoutDate", req.ExpectedPayoutDate)
	if req.Use3D {
		payload["use3d"] = true
	}
	if len(req.AllowedPaymentMethods) > 0 {
		payload["allowedPaymentMethods"] = req.AllowedPaymentMethods
	}
	return c.hostedPage(ctx, op, "/checkout", "/checkout/", payload)
}

// CreateSubscriptionCheckout opens a hosted page for a recurring payment.
func (c *Client) CreateSubscriptionCheckout(ctx context.Context, req SubscriptionCheckoutRequest) (*CheckoutSession, error) {
	const op = "CreateSubscriptionCheckout"
	currency, err := c.validatePurchase(op, req.OrderID, req.Amount, req.Currency, req.Items)
	if err != nil {
		return nil, err
	}
	successURL, failureURL, err := c.returnURLs(op, req.SuccessURL, req.FailureURL)
	if err != nil {
		return nil, err
	}
	if err := validatePeriods(op, req.NumberOfPeriods); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"merchantId":     c.cfg.MerchantID,
		"amount":         int64(req.Amount),
		"currency":       currency,
		"orderId":        req.OrderID,
		"successUrl":     successURL,
		"failureUrl":     failureURL,
		"productDetails": wireLineItems(req.Items),
	}
	addCustomer(payload, req.Customer)
	addOptional(payload, "note", req.Note)
	addOptional(payload, "customId", req.CustomID)
	if req.Use3D {
		payload["use3d"] = true
	}
	if req.NumberOfPeriods > 0 {
		payload["numberOfPeriods"] = req.NumberOfPeriods
	}
	return c.hostedPage(ctx, op, "/checkout-subscription", "/checkout/subscription/", payload)
}

// CreateScheduleCheckout opens a hosted page for a recurring payment whose
// first charge and interval the merchant chooses.
func (c *Client) CreateScheduleCheckout(ctx context.Context, req ScheduleCheckoutRequest) (*CheckoutSession, error) {
	const op = "CreateScheduleCheckout"
	currency, err := c.validatePurchase(op, req.OrderID, req.Amount, req.Currency, req.Items)
	if err != nil {
		return nil, err
	}
	successURL, failureURL, err := c.returnURLs(op, req.SuccessURL, req.FailureURL)
	if err != nil {
		return nil, err
	}
	if err := validateSchedule(op, req.NumberOfPeriods, req.PaymentInterval, req.StartDate); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"merchantId":     c.cfg.MerchantID,
		"amount":         int64(req.Amount),
		"currency":       currency,
		"orderId":        req.OrderID,
		"successUrl":     successURL,
		"failureUrl":     failureURL,
		"productDetails": wireLineItems(req.Items),
	}
	addCustomer(payload, req.Customer)
	addOptional(payload, "note", req.Note)
	addOptional(payload, "customId", req.CustomID)
	addOptional(payload, "startDate", req.StartDate)
	if req.Use3D {
		payload["use3d"] = true
	}
	if req.NumberOfPeriods > 0 {
		payload["numberOfPeriods"] = req.NumberOfPeriods
	}
	if req.PaymentInterval > 0 {
		payload["paymentInterval"] = req.PaymentInterval
	}
	return c.hostedPage(ctx, op, "/checkout-schedule", "/checkout/schedule/", payload)
}

// CreateTokenCheckout opens the hosted page that binds a card and produces a
// reusable token. The token arrives on the webhook, never in this response.
func (c *Client) CreateTokenCheckout(ctx context.Context, req TokenCheckoutRequest) (*CheckoutSession, error) {
	const op = "CreateTokenCheckout"
	successURL, failureURL, err := c.returnURLs(op, req.SuccessURL, req.FailureURL)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"merchantId": c.cfg.MerchantID,
		"successUrl": successURL,
		"failureUrl": failureURL,
	}
	addOptional(payload, "customId", req.CustomID)
	addOptional(payload, "note", req.Note)
	return c.hostedPage(ctx, op, "/checkout-token", "/checkout/subscription/create/", payload)
}

func (c *Client) hostedPage(ctx context.Context, op, path, redirectPath string, payload map[string]any) (*CheckoutSession, error) {
	resp, err := c.post(ctx, op, path, payload)
	if err != nil {
		return nil, err
	}
	var data struct {
		ID             json.RawMessage `json:"id"`
		TransactionHID json.RawMessage `json:"transactionHid"`
	}
	if err := decodeData(op, resp, &data); err != nil {
		return nil, err
	}
	id := rawString(data.ID)
	if id == "" {
		return nil, unknownResponseOutcome(op, resp, errNoField("data.id"))
	}
	return &CheckoutSession{
		ID:             id,
		TransactionHID: rawString(data.TransactionHID),
		RedirectURL:    c.checkoutURL(redirectPath, id),
	}, nil
}

func addCustomer(payload map[string]any, customer Customer) {
	addOptional(payload, "userId", customer.ID)
	addOptional(payload, "userName", customer.Name)
	addOptional(payload, "userEmail", customer.Email)
}

func addOptional(payload map[string]any, key, value string) {
	if value != "" {
		payload[key] = value
	}
}
