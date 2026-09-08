package oen

import (
	"context"
	"net/http"
	"testing"
)

func TestRefundReturnsTheRefundedTransaction(t *testing.T) {
	server, client := newFake(t)
	charged := chargeOnce(t, client, "ORDER00001")

	refunded, err := client.Refund(context.Background(), RefundRequest{
		TransactionHID: charged.TransactionHID,
		Amount:         1000,
		Items:          testItems(),
		Reason:         "客戶取消訂單",
	})
	noError(t, err)
	equal(t, charged.TransactionHID, refunded.ID)
	equal(t, StatusRefunded, refunded.Status)
	isTrue(t, refunded.Status.IsRefunded())
	isFalse(t, refunded.Status.IsPaid())
	equal(t, Amount(1000), refunded.RefundAmount)
	hasTime(t, refunded.RefundedAt)

	request := server.LastRequest("/refunds/{transactionHid}")
	equal(t, http.MethodPost, request.Method)
	equal(t, "/refunds/"+charged.TransactionHID, request.Path)
	body := bodyOf(t, request)
	jsonEqual(t, `1000`, string(body["amount"]))
	jsonEqual(t, `"客戶取消訂單"`, string(body["reason"]))
	contains(t, string(request.Body), `"productionCode":"P0001"`)
	_, hasRemit := body["remitInfo"]
	isFalse(t, hasRemit)
}

// Oen requires a bank account when the original payment was made at a
// convenience store, because there is no card to send the money back to.
func TestRefundSendsRemitInformationWhenGiven(t *testing.T) {
	server, client := newFake(t)
	charged := chargeOnce(t, client, "ORDER00001")

	_, err := client.Refund(context.Background(), RefundRequest{
		TransactionHID: charged.TransactionHID,
		Amount:         1000,
		Items:          testItems(),
		RemitInfo: &RemitInfo{
			BankCode: "812", BankName: "台新銀行",
			BranchCode: "0056", BranchName: "營業部",
			Account: "98557016831048", AccountName: "王小明",
		},
	})
	noError(t, err)
	body := bodyOf(t, server.LastRequest("/refunds/{transactionHid}"))
	jsonEqual(t,
		`{"bankCode":"812","bankName":"台新銀行","branchCode":"0056","branchName":"營業部","account":"98557016831048","accountName":"王小明"}`,
		string(body["remitInfo"]))
}

func TestRefundValidatesBeforeSending(t *testing.T) {
	tests := []struct {
		name    string
		request RefundRequest
		field   string
	}{
		{
			name:    "missing transaction",
			request: RefundRequest{Amount: 1000, Items: testItems()},
			field:   "transactionHid",
		},
		{
			name:    "zero amount",
			request: RefundRequest{TransactionHID: "P1", Items: testItems()},
			field:   "amount",
		},
		{
			name:    "no items",
			request: RefundRequest{TransactionHID: "P1", Amount: 1000},
			field:   "productDetails",
		},
		{
			name:    "items do not match the refund amount",
			request: RefundRequest{TransactionHID: "P1", Amount: 500, Items: testItems()},
			field:   "productDetails",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, client := newFake(t)
			_, err := client.Refund(context.Background(), test.request)
			hasError(t, err)
			errIs(t, err, ErrInvalidInput)
			var target *ValidationError
			isTrue(t, asValidationError(err, &target))
			equal(t, test.field, target.Field)
			equal(t, 0, len(server.Requests()))
		})
	}
}

// A refund is a state change like a charge: an uncertain answer must not turn
// into a second refund.
func TestRefundDoesNotRetryOnAnUncertainAnswer(t *testing.T) {
	server, client := newFake(t)
	charged := chargeOnce(t, client, "ORDER00001")
	server.FailHTTP("/refunds/{transactionHid}", http.StatusInternalServerError)

	_, err := client.Refund(context.Background(), RefundRequest{
		TransactionHID: charged.TransactionHID, Amount: 1000, Items: testItems(),
	})
	hasError(t, err)
	errIs(t, err, ErrUnknownOutcome)
	equal(t, 1, server.Count("/refunds/{transactionHid}"))
}

func TestRefundingAnUnknownTransactionIsAStateError(t *testing.T) {
	_, client := newFake(t)
	_, err := client.Refund(context.Background(), RefundRequest{
		TransactionHID: "P-does-not-exist", Amount: 1000, Items: testItems(),
	})
	hasError(t, err)
	errIs(t, err, ErrTransactionState)
	errIsNot(t, err, ErrUnknownOutcome)
}
