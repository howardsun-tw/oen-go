package oen

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/howardsun-tw/oen-go/oentest"
)

const testToken = "2etM3aQSCMWv7OGYQ6gDWtcOJaR"

func testCharge() TokenChargeRequest {
	return TokenChargeRequest{
		OrderID:  "ORDER00001",
		Token:    testToken,
		Amount:   1000,
		Items:    testItems(),
		Customer: Customer{Name: "王小明", Email: "test@oen.tw"},
		Note:     "first charge",
	}
}

func TestChargeTokenUsesTheDocumentedWireContract(t *testing.T) {
	server, client := newFake(t)
	result, err := client.ChargeToken(context.Background(), testCharge())
	noError(t, err)
	equal(t, oentest.DefaultTransactionHID, result.TransactionHID)
	equal(t, oentest.DefaultAuthCode, result.AuthCode)

	request := server.LastRequest("/token/transactions")
	equal(t, http.MethodPost, request.Method)
	equal(t, "Bearer "+oentest.DefaultAuthToken, request.Header.Get("Authorization"))
	body := bodyOf(t, request)
	jsonEqual(t, `"`+oentest.DefaultMerchantID+`"`, string(body["merchantId"]))
	jsonEqual(t, `1000`, string(body["amount"]))
	jsonEqual(t, `"TWD"`, string(body["currency"]))
	jsonEqual(t, `"ORDER00001"`, string(body["orderId"]))
	jsonEqual(t, `"`+testToken+`"`, string(body["token"]))
	jsonEqual(t, `"王小明"`, string(body["userName"]))
	jsonEqual(t, `"test@oen.tw"`, string(body["userEmail"]))
	jsonEqual(t, `"first charge"`, string(body["note"]))
	contains(t, string(request.Body), `"productionCode":"P0001"`)
}

func TestChargeTokenSendsInvoiceInformationWhenGiven(t *testing.T) {
	server, client := newFake(t)
	charge := testCharge()
	charge.InvoiceInfo = &InvoiceInfo{
		InvoiceType:     "company",
		CarrierType:     "",
		BuyerIdentifier: "12345678",
		BuyerName:       "應援科技",
		Email:           "invoice@oen.tw",
	}
	_, err := client.ChargeToken(context.Background(), charge)
	noError(t, err)
	body := bodyOf(t, server.LastRequest("/token/transactions"))
	jsonEqual(t,
		`{"invoiceType":"company","buyerIdentifier":"12345678","buyerName":"應援科技","email":"invoice@oen.tw"}`,
		string(body["invoiceInfo"]))

	server2, client2 := newFake(t)
	_, err = client2.ChargeToken(context.Background(), testCharge())
	noError(t, err)
	_, present := bodyOf(t, server2.LastRequest("/token/transactions"))["invoiceInfo"]
	isFalse(t, present)
}

func TestSubscribeTokenReturnsBothIdentifiers(t *testing.T) {
	server, client := newFake(t)
	result, err := client.SubscribeToken(context.Background(), TokenSubscriptionRequest{
		OrderID: "ORDER00001", Token: testToken, Amount: 1000, Items: testItems(),
		NumberOfPeriods: 12, PaymentInterval: 1, StartDate: "2026/10/01",
	})
	noError(t, err)
	equal(t, oentest.DefaultSubscriptionID, result.SubscriptionID)
	equal(t, oentest.DefaultTransactionHID, result.TransactionHID)
	equal(t, oentest.DefaultAuthCode, result.AuthCode)

	body := bodyOf(t, server.LastRequest("/token/subscriptions"))
	jsonEqual(t, `12`, string(body["numberOfPeriods"]))
	jsonEqual(t, `1`, string(body["paymentInterval"]))
	jsonEqual(t, `"2026/10/01"`, string(body["startDate"]))
	jsonEqual(t, `"`+testToken+`"`, string(body["token"]))
}

// A decline is a settled answer: the money did not move, the caller may show
// the payer a reason, and one request was sent.
func TestChargeTokenReportsDeclinesWithTheirReason(t *testing.T) {
	tests := []struct {
		code   string
		reason DeclineReason
	}{
		{code: CodeTransactionFailed, reason: ReasonTransactionFailed},
		{code: CodeInvalidCVV, reason: ReasonInvalidCVV},
		{code: CodeCardExpired, reason: ReasonCardExpired},
		{code: CodeInsufficientFunds, reason: ReasonInsufficientFunds},
		{code: CodeCardDeclined, reason: ReasonCardDeclined},
		{code: "T9999", reason: ReasonUnknown},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			server, client := newFake(t)
			server.Reject("/token/transactions", test.code, "provider declined the charge")

			_, err := client.ChargeToken(context.Background(), testCharge())
			hasError(t, err)
			errIs(t, err, ErrDeclined)
			errIsNot(t, err, ErrUnknownOutcome)
			isFalse(t, IsUnknownOutcome(err))

			var target *Error
			isTrue(t, errors.As(err, &target))
			equal(t, test.code, target.Code)
			equal(t, test.reason, target.DeclineReason())
			equal(t, "provider declined the charge", target.Message)
			equal(t, "ChargeToken", target.Op)
			equal(t, 1, server.Count("/token/transactions"))
		})
	}
}

// The failures a charge cannot distinguish from success must all end up as an
// unknown outcome, and each must cost exactly one request.
func TestChargeTokenReportsUncertainOutcomesWithoutRetrying(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*oentest.Server)
		timeout   time.Duration
	}{
		{
			name:      "server error",
			configure: func(s *oentest.Server) { s.FailHTTP("/token/transactions", http.StatusInternalServerError) },
		},
		{
			name:      "bad gateway",
			configure: func(s *oentest.Server) { s.FailHTTP("/token/transactions", http.StatusBadGateway) },
		},
		{
			name:      "provider system error code",
			configure: func(s *oentest.Server) { s.Reject("/token/transactions", CodeSystemError, "system error") },
		},
		{
			name:      "body is not JSON",
			configure: func(s *oentest.Server) { s.Malformed("/token/transactions", "<html>gateway</html>") },
		},
		{
			name:      "success body has no code",
			configure: func(s *oentest.Server) { s.Malformed("/token/transactions", `{"data":{"id":"P1"}}`) },
		},
		{
			name: "400 without a provider code",
			configure: func(s *oentest.Server) {
				s.QueueResponse("/token/transactions", oentest.Response{StatusCode: http.StatusBadRequest, Body: []byte(`{"message":"nope"}`)})
			},
		},
		{
			name:      "timeout",
			configure: func(s *oentest.Server) { s.Hang("/token/transactions") },
			timeout:   50 * time.Millisecond,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := oentest.New()
			t.Cleanup(server.Close)
			test.configure(server)
			client := newTestClient(t, server)
			if test.timeout > 0 {
				client.cfg.Timeout = test.timeout
			}

			_, err := client.ChargeToken(context.Background(), testCharge())
			hasError(t, err)
			errIs(t, err, ErrUnknownOutcome)
			errIsNot(t, err, ErrDeclined)
			isFalse(t, IsDeclined(err))
			equal(t, 1, server.Count("/token/transactions"), test.name)
		})
	}
}

// This is the outcome the whole no-retry rule exists for: Oen took the money
// and the caller never saw the answer. The SDK must not send a second charge,
// and the caller must be able to find the first one.
func TestUncertainChargeIsResolvedByQueryingNotByRetrying(t *testing.T) {
	server, client := newFake(t)
	server.ChargeThenFail("/token/transactions", http.StatusGatewayTimeout)

	_, err := client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	equal(t, 1, server.Count("/token/transactions"))

	found, err := client.ListOrderTransactions(context.Background(), "ORDER00001")
	noError(t, err)
	equal(t, 1, len(found))
	isTrue(t, found[0].Status.IsPaid())
	equal(t, Amount(1000), found[0].Amount)
	// Still one charge: the query did not create another.
	equal(t, 1, server.Count("/token/transactions"))
}

func TestChargeTokenReportsRateLimitsAsUncertain(t *testing.T) {
	server, client := newFake(t)
	server.QueueResponse("/token/transactions", oentest.Response{
		StatusCode: http.StatusTooManyRequests,
		Headers:    http.Header{"Retry-After": []string{"3"}},
	})

	_, err := client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrRateLimited)
	errIsNot(t, err, ErrDeclined)
	errIs(t, err, ErrUnknownOutcome)
	equal(t, 3*time.Second, RetryAfter(err))
	equal(t, 1, server.Count("/token/transactions"))
}

func TestAppliedChargeWithTimeoutOrRateLimitRequiresQueryBack(t *testing.T) {
	for _, test := range []struct {
		name     string
		response oentest.Response
		cause    error
	}{
		{
			name:     "timeout after charging",
			response: oentest.Response{RecordCharge: true, Block: true},
			cause:    context.DeadlineExceeded,
		},
		{
			name:     "rate limit after charging",
			response: oentest.Response{RecordCharge: true, StatusCode: http.StatusTooManyRequests},
			cause:    ErrRateLimited,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := oentest.New()
			t.Cleanup(server.Close)
			server.QueueResponse("/token/transactions", test.response)
			cfg := contractConfig(server.URL)
			cfg.Timeout = 100 * time.Millisecond
			client, err := New(cfg)
			noError(t, err)
			_, err = client.ChargeToken(context.Background(), testCharge())
			errIs(t, err, ErrUnknownOutcome)
			errIs(t, err, test.cause)
			transactions, err := client.ListOrderTransactions(context.Background(), testCharge().OrderID)
			noError(t, err)
			equal(t, 1, len(transactions))
			isTrue(t, transactions[0].Status.IsPaid())
			equal(t, 1, server.Count("/token/transactions"))
		})
	}
}

// A provider message that happens to contain "429" or "rate limit" is text,
// not a signal. Reading it as one would turn a decline into a retry.
func TestDeclineTextIsNeverReadAsARateLimit(t *testing.T) {
	server, client := newFake(t)
	server.Reject("/token/transactions", CodeCardDeclined, "rate limit 429 throttled")

	_, err := client.ChargeToken(context.Background(), testCharge())
	hasError(t, err)
	errIs(t, err, ErrDeclined)
	errIsNot(t, err, ErrRateLimited)
}

func TestChargeTokenStopsInvalidRequestsBeforeTheyAreSent(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TokenChargeRequest)
		field  string
		is     error
	}{
		{name: "missing token", mutate: func(r *TokenChargeRequest) { r.Token = "" }, field: "token"},
		{name: "missing order id", mutate: func(r *TokenChargeRequest) { r.OrderID = "" }, field: "orderId"},
		{name: "zero amount", mutate: func(r *TokenChargeRequest) { r.Amount = 0 }, field: "amount"},
		{name: "no items", mutate: func(r *TokenChargeRequest) { r.Items = nil }, field: "productDetails"},
		{
			name:   "items do not add up",
			mutate: func(r *TokenChargeRequest) { r.Amount = 1001 },
			field:  "productDetails",
			is:     ErrProductAmountMismatch,
		},
		{name: "unsupported currency", mutate: func(r *TokenChargeRequest) { r.Currency = "JPY" }, field: "currency"},
		{
			name:   "bad payout date",
			mutate: func(r *TokenChargeRequest) { r.ExpectedPayoutDate = "10/01/2026" },
			field:  "expectedPayoutDate",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, client := newFake(t)
			charge := testCharge()
			test.mutate(&charge)

			_, err := client.ChargeToken(context.Background(), charge)
			hasError(t, err)
			errIs(t, err, ErrInvalidInput)
			if test.is != nil {
				errIs(t, err, test.is)
			}
			var target *ValidationError
			isTrue(t, asValidationError(err, &target))
			equal(t, test.field, target.Field)
			equal(t, 0, server.Count("/token/transactions"))
		})
	}
}

func TestSubscribeTokenValidatesItsScheduleFields(t *testing.T) {
	server, client := newFake(t)
	_, err := client.SubscribeToken(context.Background(), TokenSubscriptionRequest{
		OrderID: "O1", Token: testToken, Amount: 1000, Items: testItems(), PaymentInterval: 13,
	})
	hasError(t, err)
	var target *ValidationError
	isTrue(t, asValidationError(err, &target))
	equal(t, "paymentInterval", target.Field)
	equal(t, 0, server.Count("/token/subscriptions"))
}

// The bearer token and the card token are the two values that must never
// reach a log file or a stored payload.
func TestSecretsNeverReachLogsOrReturnedPayloads(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	var logs []string
	client, err := New(Config{
		BaseURL:    server.URL,
		MerchantID: oentest.DefaultMerchantID,
		AuthToken:  oentest.DefaultAuthToken,
		Logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	noError(t, err)

	result, err := client.ChargeToken(context.Background(), testCharge())
	noError(t, err)

	transaction, err := client.GetTransaction(context.Background(), result.TransactionHID)
	noError(t, err)
	payload := string(transaction.RedactedPayload)
	notContains(t, payload, "4242424242424242")
	contains(t, payload, "4242")
	equal(t, "4242", transaction.PaymentInfo.CardLast4)

	log := strings.Join(logs, "\n")
	isTrue(t, len(logs) > 0)
	notContains(t, log, oentest.DefaultAuthToken)
	notContains(t, log, testToken)
	contains(t, log, "/token/transactions")
	contains(t, log, "status=200")
}

func TestChargeTokenRejectsANilContext(t *testing.T) {
	server, client := newFake(t)
	//lint:ignore SA1012 the SDK must answer a nil context instead of panicking.
	_, err := client.ChargeToken(nil, testCharge()) //nolint:staticcheck
	hasError(t, err)
	errIs(t, err, ErrInvalidInput)
	equal(t, 0, len(server.Requests()))
}

func TestChargeTokenHonoursACancelledContext(t *testing.T) {
	server, client := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.ChargeToken(ctx, testCharge())
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	errIs(t, err, context.Canceled)
	equal(t, 0, server.Count("/token/transactions"))
}
