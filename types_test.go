package oen

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAmountFormatsAndValidates(t *testing.T) {
	equal(t, "1000", Amount(1000).String())
	isTrue(t, Amount(1).Valid())
	isFalse(t, Amount(0).Valid())
	isFalse(t, Amount(-1).Valid())
}

// The documented transaction states decide whether money moved. A state the
// SDK does not know must not be reported as paid, failed or refunded.
func TestTransactionStatusClassification(t *testing.T) {
	tests := []struct {
		status   TransactionStatus
		known    bool
		pending  bool
		paid     bool
		failed   bool
		refunded bool
	}{
		{status: StatusInitiated, known: true, pending: true},
		{status: StatusCharging, known: true, pending: true},
		{status: StatusCharged, known: true, paid: true},
		{status: StatusClaimed, known: true, paid: true},
		{status: StatusFailed, known: true, failed: true},
		{status: StatusRefunded, known: true, refunded: true},
		{status: StatusRefundedPostPayout, known: true, refunded: true},
		{status: TransactionStatus("teleported")},
		{status: ""},
	}
	for _, test := range tests {
		equal(t, test.known, test.status.Known(), string(test.status))
		equal(t, test.pending, test.status.IsPending(), string(test.status))
		equal(t, test.paid, test.status.IsPaid(), string(test.status))
		equal(t, test.failed, test.status.IsFailed(), string(test.status))
		equal(t, test.refunded, test.status.IsRefunded(), string(test.status))
	}
}

func TestSubscriptionStatusClassification(t *testing.T) {
	for status, known := range map[SubscriptionStatus]bool{
		SubscriptionWaiting:   true,
		SubscriptionOngoing:   true,
		SubscriptionCancelled: true,
		SubscriptionDone:      true,
		SubscriptionError:     true,
		"reversed":            false,
	} {
		equal(t, known, status.Known(), string(status))
	}
	isTrue(t, SubscriptionOngoing.IsActive())
	isTrue(t, SubscriptionWaiting.IsActive())
	isFalse(t, SubscriptionCancelled.IsActive())
	isFalse(t, SubscriptionDone.IsActive())
	isFalse(t, SubscriptionError.IsActive())
}

// paymentInfo is the one field whose shape changes with the payment method,
// and it is the field that carries the card number.
func TestDecodePaymentInfoNeverKeepsAFullCardNumber(t *testing.T) {
	info := mustPaymentInfo(t, `{
		"cardName":"王小明",
		"cardNum":"4242424242424242",
		"cardType":"Visa",
		"method":"card"
	}`)
	equal(t, MethodCard, info.Method)
	equal(t, "4242", info.CardLast4)
	equal(t, "Visa", info.CardType)
	equal(t, "王小明", info.CardName)

	masked := mustPaymentInfo(t, `{"cardNum":"424242******4242","cardType":"Visa"}`)
	equal(t, "4242", masked.CardLast4)
	equal(t, MethodCard, masked.Method)

	atm := mustPaymentInfo(t, `{
		"bankCode":"812",
		"bankName":"台新銀行",
		"account":"98557016831048",
		"expiredAt":"2024-04-13T15:59:59.999Z",
		"method":"atm",
		"provider":"szfu",
		"sk":"171291104995271916"
	}`)
	equal(t, MethodATM, atm.Method)
	equal(t, "812", atm.BankCode)
	equal(t, "台新銀行", atm.BankName)
	equal(t, "98557016831048", atm.Account)
	hasTime(t, atm.ExpiredAt)

	cvs := mustPaymentInfo(t, `{
		"cvsName":"全家",
		"code":"ABC179356D9872",
		"expiredAt":"2025-03-27T03:41:41.000Z"
	}`)
	equal(t, MethodCVS, cvs.Method)
	equal(t, "全家", cvs.CVSName)
	equal(t, "ABC179356D9872", cvs.Code)

	// Webhooks send the last four digits as a bare string, and LINE Pay sends
	// its own transaction reference the same way.
	digits := mustPaymentInfo(t, `"4242"`)
	equal(t, "4242", digits.CardLast4)
	equal(t, "4242", digits.Text)

	reference := mustPaymentInfo(t, `"2024041200112233"`)
	equal(t, "", reference.CardLast4)
	equal(t, "2024041200112233", reference.Text)

	equal(t, PaymentInfo{}, mustPaymentInfo(t, `null`))
	equal(t, PaymentInfo{}, mustPaymentInfo(t, ``))

	// A paymentInfo that is neither a string nor an object, or whose expiry
	// cannot be read, is an error rather than a silently blank PaymentInfo.
	for _, raw := range []string{`4242`, `[1]`, `{"cardNum":"4242","expiredAt":"soon"}`} {
		_, err := decodePaymentInfo(json.RawMessage(raw), time.UTC)
		hasError(t, err, raw)
	}
}

func mustPaymentInfo(t *testing.T, raw string) PaymentInfo {
	t.Helper()
	info, err := decodePaymentInfo(json.RawMessage(raw), time.UTC)
	noError(t, err)
	return info
}

// This is the exact resource from Oen's documentation, so the decoder is
// checked against the provider's own example rather than an invented one.
func TestDecodeTransactionReadsTheDocumentedResource(t *testing.T) {
	resource := json.RawMessage(`{
		"id": "P20240412QJMAZNML",
		"transactionId": "2ezVvlPIKZzJxY8L2ukhEPKDJWz",
		"action": "onetime",
		"amount": 1000,
		"fee": 30,
		"paymentInfo": {
			"cardType": "Visa",
			"cardNum": "4242424242424242",
			"method": "card",
			"cardName": "王小明"
		},
		"status": "charged",
		"userId": "OEN00001",
		"userName": "王小明",
		"userEmail": "test@oen.tw",
		"orderId": "ORDER00001",
		"note": "備註",
		"createdAt": "2024-04-12T07:40:25.502Z",
		"refundAmount": 0
	}`)

	transaction, err := decodeTransaction(resource, time.UTC)
	noError(t, err)
	equal(t, "P20240412QJMAZNML", transaction.ID)
	equal(t, "2ezVvlPIKZzJxY8L2ukhEPKDJWz", transaction.TransactionID)
	equal(t, ActionOneTime, transaction.Action)
	equal(t, Amount(1000), transaction.Amount)
	equal(t, Amount(30), transaction.Fee)
	equal(t, Amount(0), transaction.RefundAmount)
	equal(t, StatusCharged, transaction.Status)
	isTrue(t, transaction.Status.IsPaid())
	equal(t, "OEN00001", transaction.Customer.ID)
	equal(t, "ORDER00001", transaction.OrderID)
	equal(t, "備註", transaction.Note)
	equal(t, "4242", transaction.PaymentInfo.CardLast4)
	equal(t, time.Date(2024, 4, 12, 7, 40, 25, 502000000, time.UTC), transaction.CreatedAt.UTC())
	equal(t, (*time.Time)(nil), transaction.RefundedAt)
	notContains(t, string(transaction.RedactedPayload), "4242424242424242")
	contains(t, string(transaction.RedactedPayload), "P20240412QJMAZNML")
}

func TestDecodeTransactionReadsRefundAndSubscriptionFields(t *testing.T) {
	transaction, err := decodeTransaction(json.RawMessage(`{
		"id":"P20240411AN3KW7BK",
		"action":"subscription",
		"status":"refunded",
		"amount":1000,
		"refundAmount":1000,
		"refundedAt":"2024-04-11T10:45:22.076Z",
		"authCode":"831000",
		"subscriptionId":"S20240412YOARH7GM",
		"period":2,
		"reason":"客戶取消訂單",
		"payoutId":"PO001",
		"payoutAt":"2024-04-20T00:00:00.000Z",
		"createdAt":"2024-04-11T10:45:22.026Z"
	}`), time.UTC)
	noError(t, err)
	equal(t, ActionSubscription, transaction.Action)
	equal(t, StatusRefunded, transaction.Status)
	isTrue(t, transaction.Status.IsRefunded())
	equal(t, Amount(1000), transaction.RefundAmount)
	hasTime(t, transaction.RefundedAt)
	equal(t, "831000", transaction.AuthCode)
	equal(t, "S20240412YOARH7GM", transaction.SubscriptionID)
	equal(t, 2, transaction.Period)
	equal(t, "客戶取消訂單", transaction.Reason)
	equal(t, "PO001", transaction.PayoutID)
	hasTime(t, transaction.PayoutAt)
}

func TestDecodeTransactionRejectsAResourceWithoutAnIdentity(t *testing.T) {
	_, err := decodeTransaction(json.RawMessage(`{"amount":100}`), time.UTC)
	hasError(t, err)
	_, err = decodeTransaction(json.RawMessage(`not json`), time.UTC)
	hasError(t, err)
	_, err = decodeTransaction(json.RawMessage(`{"id":"P1","amount":"twelve"}`), time.UTC)
	hasError(t, err)
}

func TestDecodeSubscriptionReadsTheDocumentedResource(t *testing.T) {
	subscription, err := decodeSubscription(json.RawMessage(`{
		"id": "S2024041277SVMQE5",
		"status": "ongoing",
		"period": 1,
		"startedAt": "2024-04-12T07:44:35.592Z",
		"nextChargeAt": "2024-05-11T16:00:00.000Z",
		"createdAt": "2024-04-12T07:44:35.592Z",
		"amount": 1000,
		"userId": "OEN00001",
		"userName": "王小明",
		"orderId": "ORDER00001",
		"note": "備註"
	}`), time.UTC, true)
	noError(t, err)
	equal(t, "S2024041277SVMQE5", subscription.ID)
	equal(t, SubscriptionOngoing, subscription.Status)
	equal(t, 1, subscription.Period)
	equal(t, Amount(1000), subscription.Amount)
	equal(t, "ORDER00001", subscription.OrderID)
	hasTime(t, subscription.NextChargeAt)
	equal(t, (*time.Time)(nil), subscription.CancelledAt)

	cancelled, err := decodeSubscription(json.RawMessage(`{
		"id":"S202404112CYPRJPZ",
		"status":"cancelled",
 "amount":1000,
		"period":1,
		"startedAt":"2024-04-11T10:31:46.640Z",
		"cancelledAt":"2024-04-11T10:31:46.744Z",
		"reason":"客戶自行取消",
		"createdAt":"2024-04-11T10:31:46.640Z"
	}`), time.UTC, true)
	noError(t, err)
	equal(t, SubscriptionCancelled, cancelled.Status)
	equal(t, "客戶自行取消", cancelled.Reason)
	hasTime(t, cancelled.CancelledAt)

	_, err = decodeSubscription(json.RawMessage(`{"status":"ongoing"}`), time.UTC, true)
	hasError(t, err)
}

func TestLineItemsSerializeToTheDocumentedWireNames(t *testing.T) {
	encoded, err := json.Marshal(wireLineItems([]LineItem{{
		ProductionCode: "P0001",
		Description:    "商品描述",
		Quantity:       1,
		Unit:           "個",
		UnitPrice:      1000,
	}}))
	noError(t, err)
	jsonEqual(t, `[{"productionCode":"P0001","description":"商品描述","quantity":1,"unit":"個","unitPrice":1000}]`, string(encoded))
}

func hasTime(t *testing.T, value *time.Time) {
	t.Helper()
	if value == nil || value.IsZero() {
		t.Fatalf("want a timestamp, got %v", value)
	}
}
