package oen

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/howardsun-tw/oen-go/oentest"
)

func chargeOnce(t *testing.T, client *Client, orderID string) *ChargeResult {
	t.Helper()
	charge := testCharge()
	charge.OrderID = orderID
	result, err := client.ChargeToken(context.Background(), charge)
	noError(t, err)
	return result
}

func TestGetTransactionReadsTheDocumentedResource(t *testing.T) {
	server, client := newFake(t)
	charged := chargeOnce(t, client, "ORDER00001")

	transaction, err := client.GetTransaction(context.Background(), charged.TransactionHID)
	noError(t, err)
	equal(t, charged.TransactionHID, transaction.ID)
	equal(t, oentest.DefaultTransactionID, transaction.TransactionID)
	equal(t, StatusCharged, transaction.Status)
	equal(t, ActionOneTime, transaction.Action)
	equal(t, Amount(1000), transaction.Amount)
	equal(t, "ORDER00001", transaction.OrderID)
	equal(t, MethodCard, transaction.PaymentInfo.Method)
	equal(t, "4242", transaction.PaymentInfo.CardLast4)

	request := server.LastRequest("/transactions/{id}")
	equal(t, http.MethodGet, request.Method)
	equal(t, "/transactions/"+charged.TransactionHID, request.Path)
	equal(t, 0, len(request.Body))
}

// Identifiers come from the provider and from callers; a slash in one must
// not be able to reach a different endpoint.
func TestGetTransactionEscapesTheIdentifier(t *testing.T) {
	server, client := newFake(t)
	_, err := client.GetTransaction(context.Background(), "../order/ORDER00001/transactions")
	hasError(t, err)
	request := server.Requests()[0]
	equal(t, "/transactions/..%2Forder%2FORDER00001%2Ftransactions", request.RawPath)
}

func TestGetTransactionRequiresAnIdentifier(t *testing.T) {
	server, client := newFake(t)
	_, err := client.GetTransaction(context.Background(), "  ")
	hasError(t, err)
	errIs(t, err, ErrInvalidInput)
	equal(t, 0, len(server.Requests()))
}

// Oen answers a missing transaction with a business code, not a 404, so the
// caller sees a typed error rather than an empty transaction.
func TestGetTransactionReportsAnUnknownTransaction(t *testing.T) {
	_, client := newFake(t)
	_, err := client.GetTransaction(context.Background(), "P-does-not-exist")
	hasError(t, err)
	errIs(t, err, ErrTransactionState)
}

func TestListOrderTransactionsReturnsEveryAttempt(t *testing.T) {
	server, client := newFake(t)
	first := chargeOnce(t, client, "ORDER00001")
	second := chargeOnce(t, client, "ORDER00001")
	chargeOnce(t, client, "ORDER00002")

	transactions, err := client.ListOrderTransactions(context.Background(), "ORDER00001")
	noError(t, err)
	equal(t, 2, len(transactions))
	equal(t, first.TransactionHID, transactions[0].ID)
	equal(t, second.TransactionHID, transactions[1].ID)
	equal(t, "/order/ORDER00001/transactions", server.LastRequest("/order/{orderId}/transactions").Path)

	empty, err := client.ListOrderTransactions(context.Background(), "ORDER-NOTHING")
	noError(t, err)
	equal(t, 0, len(empty))
}

func TestListOrderTransactionsRequiresAnOrderID(t *testing.T) {
	server, client := newFake(t)
	_, err := client.ListOrderTransactions(context.Background(), "")
	hasError(t, err)
	errIs(t, err, ErrInvalidInput)
	equal(t, 0, len(server.Requests()))
}

// Oen returns lists in more than one envelope shape across its endpoints, and
// a shape the SDK cannot read must be an error rather than an empty list: an
// empty list would read as "no charge happened".
func TestTransactionListsAcceptEveryDocumentedShape(t *testing.T) {
	for _, data := range []string{
		`{"transactions":[{"id":"P1","amount":1000,"status":"charged"}]}`,
		`[{"id":"P1","amount":1000,"status":"charged"}]`,
		`{"items":[{"id":"P1","amount":1000,"status":"charged"}]}`,
		`{"data":[{"id":"P1","amount":1000,"status":"charged"}]}`,
	} {
		server, client := newFake(t)
		server.QueueResponse("/order/{orderId}/transactions", oentest.Response{
			Body: []byte(`{"code":"S0000","message":"","data":` + data + `}`),
		})
		transactions, err := client.ListOrderTransactions(context.Background(), "ORDER00001")
		noError(t, err)
		equal(t, 1, len(transactions), data)
		equal(t, "P1", transactions[0].ID)
	}

	server, client := newFake(t)
	server.QueueResponse("/order/{orderId}/transactions", oentest.Response{
		Body: []byte(`{"code":"S0000","data":{"unexpected":"shape"}}`),
	})
	_, err := client.ListOrderTransactions(context.Background(), "ORDER00001")
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
}

func TestListTransactionsSendsItsQueryAndReadsThePageToken(t *testing.T) {
	server, client := newFake(t)
	chargeOnce(t, client, "ORDER00001")
	server.SetNextPage("eyJMaW1pdCI6MTAwfQ==")

	page, err := client.ListTransactions(context.Background(), ListTransactionsRequest{
		Start: "2026-09-01T00:00:00Z",
		End:   "2026-09-30T23:59:59Z",
		Page:  "previous-token",
	})
	noError(t, err)
	equal(t, 1, len(page.Transactions))
	equal(t, "eyJMaW1pdCI6MTAwfQ==", page.NextPage)

	query, err := url.ParseQuery(server.LastRequest("/transactions").Query)
	noError(t, err)
	equal(t, "2026-09-01T00:00:00Z", query.Get("start"))
	equal(t, "2026-09-30T23:59:59Z", query.Get("end"))
	equal(t, "previous-token", query.Get("page"))
}

func TestListTransactionsOmitsEmptyQueryParameters(t *testing.T) {
	server, client := newFake(t)
	_, err := client.ListTransactions(context.Background(), ListTransactionsRequest{})
	noError(t, err)
	equal(t, "", server.LastRequest("/transactions").Query)
}

func TestGetSubscriptionReadsTheDocumentedResource(t *testing.T) {
	server, client := newFake(t)
	started, err := client.SubscribeToken(context.Background(), TokenSubscriptionRequest{
		OrderID: "ORDER00001", Token: testToken, Amount: 1000, Items: testItems(), NumberOfPeriods: 12,
	})
	noError(t, err)

	subscription, err := client.GetSubscription(context.Background(), started.SubscriptionID)
	noError(t, err)
	equal(t, started.SubscriptionID, subscription.ID)
	equal(t, SubscriptionOngoing, subscription.Status)
	isTrue(t, subscription.Status.IsActive())
	equal(t, Amount(1000), subscription.Amount)
	equal(t, 12, subscription.NumberOfPeriods)
	equal(t, "ORDER00001", subscription.OrderID)
	hasTime(t, subscription.NextChargeAt)
	equal(t, http.MethodGet, server.LastRequest("/subscriptions/{subscriptionId}").Method)
}

func TestCancelSubscriptionSendsTheDocumentedBody(t *testing.T) {
	server, client := newFake(t)
	started, err := client.SubscribeToken(context.Background(), TokenSubscriptionRequest{
		OrderID: "ORDER00001", Token: testToken, Amount: 1000, Items: testItems(),
	})
	noError(t, err)

	cancelled, err := client.CancelSubscription(context.Background(), CancelSubscriptionRequest{
		SubscriptionID: started.SubscriptionID,
		Reason:         "客戶自行取消",
	})
	noError(t, err)
	equal(t, SubscriptionCancelled, cancelled.Status)
	isFalse(t, cancelled.Status.IsActive())
	equal(t, "客戶自行取消", cancelled.Reason)
	hasTime(t, cancelled.CancelledAt)

	request := server.LastRequest("/subscriptions/{subscriptionId}")
	equal(t, http.MethodPut, request.Method)
	body := bodyOf(t, request)
	jsonEqual(t, `"`+oentest.DefaultMerchantID+`"`, string(body["merchantId"]))
	jsonEqual(t, `"客戶自行取消"`, string(body["reason"]))

	// Cancelling again is a state error, not a silent success.
	_, err = client.CancelSubscription(context.Background(), CancelSubscriptionRequest{
		SubscriptionID: "S-does-not-exist",
	})
	hasError(t, err)
	errIs(t, err, ErrTransactionState)
}

func TestCancelSubscriptionRequiresAnIdentifier(t *testing.T) {
	server, client := newFake(t)
	_, err := client.CancelSubscription(context.Background(), CancelSubscriptionRequest{Reason: "no id"})
	hasError(t, err)
	errIs(t, err, ErrInvalidInput)
	equal(t, 0, len(server.Requests()))
}

// A read is safe to repeat, so a query that fails may be retried by the
// caller; it still must never be retried inside the SDK.
func TestQueriesDoNotRetryEither(t *testing.T) {
	server, client := newFake(t)
	server.FailHTTP("/transactions/{id}", http.StatusInternalServerError)
	_, err := client.GetTransaction(context.Background(), "P1")
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	equal(t, 1, server.Count("/transactions/P1"))
}

func TestQueriesRespectTheConfiguredTimeout(t *testing.T) {
	server, client := newFake(t)
	client.cfg.Timeout = 50 * time.Millisecond
	server.Hang("/transactions/{id}")

	started := time.Now()
	_, err := client.GetTransaction(context.Background(), "P1")
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	isTrue(t, time.Since(started) < 2*time.Second)
}

func TestCancellationDistinguishesOmittedAmountFromZero(t *testing.T) {
	for _, test := range []struct {
		name      string
		field     string
		hasAmount bool
		invalid   bool
	}{
		{name: "omitted as in the official example"},
		{name: "null", field: `,"amount":null`},
		{name: "explicit zero", field: `,"amount":0`, hasAmount: true},
		{name: "invalid supplied amount", field: `,"amount":"invalid"`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, client := newFake(t)
			server.QueueResponse("PUT /subscriptions/{subscriptionId}", oentest.Response{
				Body: []byte(`{"code":"S0000","data":{"id":"S1","status":"cancelled"` + test.field + `}}`),
			})
			result, err := client.CancelSubscription(context.Background(), CancelSubscriptionRequest{SubscriptionID: "S1"})
			if test.invalid {
				errIs(t, err, ErrUnknownOutcome)
				return
			}
			noError(t, err)
			equal(t, SubscriptionCancelled, result.Status)
			equal(t, test.hasAmount, result.HasAmount)
			equal(t, Amount(0), result.Amount)
		})
	}
}
