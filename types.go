package oen

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CurrencyTWD is the only currency Oen documents.
const CurrencyTWD = "TWD"

// Amount is a whole-currency amount, never a minor unit: Oen's amount fields
// are New Taiwan dollars.
type Amount int64

func (a Amount) String() string { return strconv.FormatInt(int64(a), 10) }

// Valid reports whether the amount can be charged.
func (a Amount) Valid() bool { return a > 0 }

// PaymentMethod is Oen's payment method slug.
type PaymentMethod string

const (
	MethodCard    PaymentMethod = "card"
	MethodATM     PaymentMethod = "atm"
	MethodCVS     PaymentMethod = "cvs"
	MethodLinePay PaymentMethod = "linePay"
)

// Action tells a one-off transaction apart from one instalment of a
// subscription.
type Action string

const (
	ActionOneTime      Action = "onetime"
	ActionSubscription Action = "subscription"
)

// TransactionStatus is Oen's documented transaction state.
type TransactionStatus string

const (
	StatusInitiated          TransactionStatus = "initiated"          // payment intent created (已建立付款意向)
	StatusCharging           TransactionStatus = "charging"           // payment in progress (付款中)
	StatusCharged            TransactionStatus = "charged"            // paid (已付款)
	StatusFailed             TransactionStatus = "failed"             // payment failed (付款失敗)
	StatusClaimed            TransactionStatus = "claimed"            // paid out to the merchant (已撥款)
	StatusRefunded           TransactionStatus = "refunded"           // refunded (已退款)
	StatusRefundedPostPayout TransactionStatus = "refundedPostPayout" // refunded after payout (撥款後退款)
)

// Known reports whether the status is one this SDK version documents. An
// unknown status answers false to every other question here, so new provider
// states can never be mistaken for a settled outcome.
func (s TransactionStatus) Known() bool {
	switch s {
	case StatusInitiated, StatusCharging, StatusCharged, StatusFailed,
		StatusClaimed, StatusRefunded, StatusRefundedPostPayout:
		return true
	default:
		return false
	}
}

// IsPending reports a transaction that has not reached an outcome yet.
func (s TransactionStatus) IsPending() bool {
	return s == StatusInitiated || s == StatusCharging
}

// IsPaid reports a transaction whose money moved and stayed.
func (s TransactionStatus) IsPaid() bool {
	return s == StatusCharged || s == StatusClaimed
}

// IsFailed reports a transaction that did not take the money.
func (s TransactionStatus) IsFailed() bool { return s == StatusFailed }

// IsRefunded reports a transaction that took the money and gave it back.
func (s TransactionStatus) IsRefunded() bool {
	return s == StatusRefunded || s == StatusRefundedPostPayout
}

// SubscriptionStatus is Oen's documented subscription state.
type SubscriptionStatus string

const (
	SubscriptionWaiting   SubscriptionStatus = "waiting"   // not started yet (尚未開始)
	SubscriptionOngoing   SubscriptionStatus = "ongoing"   // in progress (進行中)
	SubscriptionCancelled SubscriptionStatus = "cancelled" // cancelled (已取消)
	SubscriptionDone      SubscriptionStatus = "done"      // finished (已結束)
	SubscriptionError     SubscriptionStatus = "error"     // abnormal (異常)
)

// Known reports whether the status is one this SDK version documents.
func (s SubscriptionStatus) Known() bool {
	switch s {
	case SubscriptionWaiting, SubscriptionOngoing, SubscriptionCancelled, SubscriptionDone, SubscriptionError:
		return true
	default:
		return false
	}
}

// IsActive reports a subscription Oen will still charge.
func (s SubscriptionStatus) IsActive() bool {
	return s == SubscriptionWaiting || s == SubscriptionOngoing
}

// LineItem is one entry of Oen's productDetails. Oen requires the line items
// to add up to the request amount.
type LineItem struct {
	ProductionCode string // product code (商品編號)
	Description    string // product description (商品描述)
	Quantity       int    // quantity (數量)
	Unit           string // unit (單位)
	UnitPrice      Amount // unit price (單價)
}

// Customer carries the optional consumer fields. Oen requires Name and Email
// when it issues the invoice on the merchant's behalf.
type Customer struct {
	ID    string // userId
	Name  string // userName
	Email string // userEmail
}

// InvoiceInfo describes how Oen should issue the invoice for a token charge.
type InvoiceInfo struct {
	InvoiceType     string // cloud | company
	CarrierType     string // 3J0002 | CQ0001 | ""
	CarrierID       string
	BuyerIdentifier string // business tax ID (統一編號)
	BuyerName       string
	Email           string
}

// RemitInfo is the bank account Oen refunds to. Oen requires it when the
// original transaction was paid at a convenience store.
type RemitInfo struct {
	BankCode    string
	BankName    string
	BranchCode  string
	BranchName  string
	Account     string
	AccountName string
}

// PaymentInfo is Oen's paymentInfo field. Its shape depends on the payment
// method, and card numbers are reduced to their last four digits before they
// leave the SDK.
type PaymentInfo struct {
	Method    PaymentMethod
	CardLast4 string
	CardType  string
	CardName  string

	BankCode string
	BankName string
	Account  string

	CVSName string
	Code    string

	ExpiredAt *time.Time

	// Text holds the value as sent when Oen used a bare string: the card's
	// last four digits on webhooks, or LINE Pay's own transaction reference.
	Text string
}

// Transaction is Oen's transaction resource.
type Transaction struct {
	// ID is the transaction HID, the P-prefixed identifier refunds take.
	ID string
	// TransactionID is Oen's other identifier for the same transaction.
	TransactionID string

	Action Action
	Status TransactionStatus

	Amount       Amount
	Fee          Amount
	RefundAmount Amount
	// Currency is empty on resources: Oen does not return it.
	Currency string

	AuthCode string
	OrderID  string
	Customer Customer
	Note     string
	// Reason is Oen's failure or cancellation reason.
	Reason string

	PaymentInfo PaymentInfo

	CreatedAt  time.Time
	PaidAt     *time.Time
	RefundedAt *time.Time

	PayoutID string
	PayoutAt *time.Time

	SubscriptionID string
	Period         int

	// RedactedPayload is the provider resource with secrets removed and card
	// numbers reduced. It is safe to log or store.
	RedactedPayload json.RawMessage
}

// Subscription is Oen's subscription resource.
type Subscription struct {
	ID     string
	Status SubscriptionStatus
	Amount Amount
	// HasAmount distinguishes an omitted amount from zero. The documented
	// cancellation response omits amount; query responses must include it.
	HasAmount bool

	Period          int
	NumberOfPeriods int

	StartedAt    time.Time
	CreatedAt    time.Time
	NextChargeAt *time.Time
	CancelledAt  *time.Time

	Customer Customer
	OrderID  string
	Reason   string
	Note     string

	RedactedPayload json.RawMessage
}

type wireProductDetail struct {
	ProductionCode string `json:"productionCode"`
	Description    string `json:"description"`
	Quantity       int    `json:"quantity"`
	Unit           string `json:"unit"`
	UnitPrice      int64  `json:"unitPrice"`
}

func wireLineItems(items []LineItem) []wireProductDetail {
	if items == nil {
		return nil
	}
	details := make([]wireProductDetail, len(items))
	for i, item := range items {
		details[i] = wireProductDetail{
			ProductionCode: item.ProductionCode,
			Description:    item.Description,
			Quantity:       item.Quantity,
			Unit:           item.Unit,
			UnitPrice:      int64(item.UnitPrice),
		}
	}
	return details
}

type wireInvoiceInfo struct {
	InvoiceType     string `json:"invoiceType"`
	CarrierType     string `json:"carrierType"`
	CarrierID       string `json:"carrierId,omitempty"`
	BuyerIdentifier string `json:"buyerIdentifier,omitempty"`
	BuyerName       string `json:"buyerName,omitempty"`
	Email           string `json:"email,omitempty"`
}

type wireRemitInfo struct {
	BankCode    string `json:"bankCode"`
	BankName    string `json:"bankName"`
	BranchCode  string `json:"branchCode"`
	BranchName  string `json:"branchName"`
	Account     string `json:"account"`
	AccountName string `json:"accountName"`
}

type wireTransaction struct {
	ID             json.RawMessage `json:"id"`
	TransactionID  json.RawMessage `json:"transactionId"`
	TransactionHID json.RawMessage `json:"transactionHid"`
	Action         json.RawMessage `json:"action"`
	Status         json.RawMessage `json:"status"`
	Amount         json.RawMessage `json:"amount"`
	Fee            json.RawMessage `json:"fee"`
	RefundAmount   json.RawMessage `json:"refundAmount"`
	Currency       json.RawMessage `json:"currency"`
	AuthCode       json.RawMessage `json:"authCode"`
	OrderID        json.RawMessage `json:"orderId"`
	UserID         json.RawMessage `json:"userId"`
	UserName       json.RawMessage `json:"userName"`
	UserEmail      json.RawMessage `json:"userEmail"`
	Note           json.RawMessage `json:"note"`
	Reason         json.RawMessage `json:"reason"`
	PaymentInfo    json.RawMessage `json:"paymentInfo"`
	PaymentMethod  json.RawMessage `json:"paymentMethod"`
	CreatedAt      json.RawMessage `json:"createdAt"`
	PaidAt         json.RawMessage `json:"paidAt"`
	RefundedAt     json.RawMessage `json:"refundedAt"`
	PayoutID       json.RawMessage `json:"payoutId"`
	PayoutAt       json.RawMessage `json:"payoutAt"`
	SubscriptionID json.RawMessage `json:"subscriptionId"`
	Period         json.RawMessage `json:"period"`
}

func decodeTransaction(resource json.RawMessage, loc *time.Location) (Transaction, error) {
	clean, err := SanitizeJSON(resource)
	if err != nil {
		return Transaction{}, fmt.Errorf("sanitize transaction: %w", err)
	}
	var wire wireTransaction
	if err := json.Unmarshal(clean, &wire); err != nil {
		return Transaction{}, fmt.Errorf("decode transaction: %w", err)
	}
	id := firstNonEmpty(rawString(wire.ID), rawString(wire.TransactionHID))
	if id == "" {
		return Transaction{}, fmt.Errorf("transaction is missing id")
	}
	amount, err := parseAmount(wire.Amount)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction amount: %w", err)
	}
	fee, err := parseOptionalAmount(wire.Fee)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction fee: %w", err)
	}
	refundAmount, err := parseOptionalAmount(wire.RefundAmount)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction refundAmount: %w", err)
	}
	info, err := decodePaymentInfo(wire.PaymentInfo, loc)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction paymentInfo: %w", err)
	}
	if info.Method == "" {
		info.Method = PaymentMethod(rawString(wire.PaymentMethod))
	}
	period, err := parseOptionalInt(wire.Period)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction period: %w", err)
	}
	createdAt, err := parseTime(wire.CreatedAt, loc)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction createdAt: %w", err)
	}
	paidAt, err := optionalTime(wire.PaidAt, loc)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction paidAt: %w", err)
	}
	refundedAt, err := optionalTime(wire.RefundedAt, loc)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction refundedAt: %w", err)
	}
	payoutAt, err := optionalTime(wire.PayoutAt, loc)
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction payoutAt: %w", err)
	}
	return Transaction{
		ID:            id,
		TransactionID: rawString(wire.TransactionID),
		Action:        Action(rawString(wire.Action)),
		Status:        TransactionStatus(rawString(wire.Status)),
		Amount:        amount,
		Fee:           fee,
		RefundAmount:  refundAmount,
		Currency:      rawString(wire.Currency),
		AuthCode:      rawString(wire.AuthCode),
		OrderID:       rawString(wire.OrderID),
		Customer: Customer{
			ID:    rawString(wire.UserID),
			Name:  rawString(wire.UserName),
			Email: rawString(wire.UserEmail),
		},
		Note:            rawString(wire.Note),
		Reason:          rawString(wire.Reason),
		PaymentInfo:     info,
		CreatedAt:       createdAt,
		PaidAt:          paidAt,
		RefundedAt:      refundedAt,
		PayoutID:        rawString(wire.PayoutID),
		PayoutAt:        payoutAt,
		SubscriptionID:  rawString(wire.SubscriptionID),
		Period:          period,
		RedactedPayload: clean,
	}, nil
}

type wireSubscription struct {
	ID              json.RawMessage `json:"id"`
	Status          json.RawMessage `json:"status"`
	Amount          json.RawMessage `json:"amount"`
	Period          json.RawMessage `json:"period"`
	NumberOfPeriods json.RawMessage `json:"numberOfPeriods"`
	StartedAt       json.RawMessage `json:"startedAt"`
	CreatedAt       json.RawMessage `json:"createdAt"`
	NextChargeAt    json.RawMessage `json:"nextChargeAt"`
	CancelledAt     json.RawMessage `json:"cancelledAt"`
	UserID          json.RawMessage `json:"userId"`
	UserName        json.RawMessage `json:"userName"`
	OrderID         json.RawMessage `json:"orderId"`
	Reason          json.RawMessage `json:"reason"`
	Note            json.RawMessage `json:"note"`
}

func decodeSubscription(resource json.RawMessage, loc *time.Location, requireAmount bool) (Subscription, error) {
	var wire wireSubscription
	if err := json.Unmarshal(resource, &wire); err != nil {
		return Subscription{}, fmt.Errorf("decode subscription: %w", err)
	}
	id := rawString(wire.ID)
	if id == "" {
		return Subscription{}, fmt.Errorf("subscription is missing id")
	}
	hasAmount := !isNullRaw(wire.Amount)
	if requireAmount && !hasAmount {
		return Subscription{}, fmt.Errorf("subscription amount: amount is missing")
	}
	amount, err := parseOptionalAmount(wire.Amount)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription amount: %w", err)
	}
	period, err := parseOptionalInt(wire.Period)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription period: %w", err)
	}
	numberOfPeriods, err := parseOptionalInt(wire.NumberOfPeriods)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription numberOfPeriods: %w", err)
	}
	startedAt, err := parseTime(wire.StartedAt, loc)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription startedAt: %w", err)
	}
	createdAt, err := parseTime(wire.CreatedAt, loc)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription createdAt: %w", err)
	}
	nextChargeAt, err := optionalTime(wire.NextChargeAt, loc)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription nextChargeAt: %w", err)
	}
	cancelledAt, err := optionalTime(wire.CancelledAt, loc)
	if err != nil {
		return Subscription{}, fmt.Errorf("subscription cancelledAt: %w", err)
	}
	return Subscription{
		ID:              id,
		Status:          SubscriptionStatus(rawString(wire.Status)),
		Amount:          amount,
		HasAmount:       hasAmount,
		Period:          period,
		NumberOfPeriods: numberOfPeriods,
		StartedAt:       startedAt,
		CreatedAt:       createdAt,
		NextChargeAt:    nextChargeAt,
		CancelledAt:     cancelledAt,
		Customer: Customer{
			ID:   rawString(wire.UserID),
			Name: rawString(wire.UserName),
		},
		OrderID:         rawString(wire.OrderID),
		Reason:          rawString(wire.Reason),
		Note:            rawString(wire.Note),
		RedactedPayload: redactedResource(resource),
	}, nil
}

type wirePaymentInfo struct {
	Method   json.RawMessage `json:"method"`
	CardNum  json.RawMessage `json:"cardNum"`
	CardType json.RawMessage `json:"cardType"`
	CardName json.RawMessage `json:"cardName"`

	BankCode json.RawMessage `json:"bankCode"`
	BankName json.RawMessage `json:"bankName"`
	Account  json.RawMessage `json:"account"`

	CVSName json.RawMessage `json:"cvsName"`
	Code    json.RawMessage `json:"code"`

	ExpiredAt json.RawMessage `json:"expiredAt"`
}

// decodePaymentInfo reads Oen's paymentInfo field. An absent value is an
// empty PaymentInfo; a value that is neither a string nor an object, or that
// carries an unreadable expiredAt, is an error rather than a silent blank.
func decodePaymentInfo(raw json.RawMessage, loc *time.Location) (PaymentInfo, error) {
	if isNullRaw(raw) {
		return PaymentInfo{}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.TrimSpace(text)
		info := PaymentInfo{Text: text}
		if isFourDigits(text) {
			info.CardLast4 = text
		}
		return info, nil
	}
	var wire wirePaymentInfo
	if err := json.Unmarshal(raw, &wire); err != nil {
		return PaymentInfo{}, fmt.Errorf("decode paymentInfo: %w", err)
	}
	expiredAt, err := optionalTime(wire.ExpiredAt, loc)
	if err != nil {
		return PaymentInfo{}, fmt.Errorf("paymentInfo expiredAt: %w", err)
	}
	info := PaymentInfo{
		Method:    PaymentMethod(rawString(wire.Method)),
		CardLast4: lastFour(rawString(wire.CardNum)),
		CardType:  rawString(wire.CardType),
		CardName:  rawString(wire.CardName),
		BankCode:  rawString(wire.BankCode),
		BankName:  rawString(wire.BankName),
		Account:   rawString(wire.Account),
		CVSName:   rawString(wire.CVSName),
		Code:      rawString(wire.Code),
		ExpiredAt: expiredAt,
	}
	if info.Method == "" {
		switch {
		case info.CardLast4 != "" || info.CardType != "":
			info.Method = MethodCard
		case info.Account != "" || info.BankCode != "":
			info.Method = MethodATM
		case info.CVSName != "" || info.Code != "":
			info.Method = MethodCVS
		}
	}
	return info, nil
}

func isFourDigits(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
