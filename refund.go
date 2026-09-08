package oen

import (
	"context"
	"strings"
)

// RefundRequest returns money from one transaction (退款).
//
// Oen refunds against the transaction HID, and the line items must describe
// what is being refunded and add up to Amount.
type RefundRequest struct {
	// TransactionHID is the transaction to refund, [Transaction.ID].
	TransactionHID string
	// Amount is at least 1 and at most the original amount. Oen decides the
	// upper bound; the SDK only checks that it is positive.
	Amount Amount
	// Items describe the refunded goods and must add up to Amount.
	Items []LineItem
	// RemitInfo is the bank account to remit to. Oen requires it when the
	// original transaction was paid at a convenience store.
	RemitInfo *RemitInfo
	// Reason is stored by Oen and shown in its console.
	Reason string
}

// Refund returns money from a transaction and answers with the transaction in
// its new state. The request is sent exactly once; on [ErrUnknownOutcome],
// read the transaction with [Client.GetTransaction] before refunding again.
func (c *Client) Refund(ctx context.Context, req RefundRequest) (*Transaction, error) {
	const op = "Refund"
	if strings.TrimSpace(req.TransactionHID) == "" {
		return nil, newValidationError(op, "transactionHid", "transaction HID is required")
	}
	if !req.Amount.Valid() {
		return nil, newValidationError(op, "amount", "refund amount must be positive")
	}
	if err := validateItems(op, req.Amount, req.Items); err != nil {
		return nil, err
	}

	payload := map[string]any{
		"merchantId":     c.cfg.MerchantID,
		"amount":         int64(req.Amount),
		"productDetails": wireLineItems(req.Items),
	}
	addOptional(payload, "reason", req.Reason)
	if req.RemitInfo != nil {
		payload["remitInfo"] = wireRemitInfo{
			BankCode:    req.RemitInfo.BankCode,
			BankName:    req.RemitInfo.BankName,
			BranchCode:  req.RemitInfo.BranchCode,
			BranchName:  req.RemitInfo.BranchName,
			Account:     req.RemitInfo.Account,
			AccountName: req.RemitInfo.AccountName,
		}
	}

	resp, err := c.post(ctx, op, "/refunds/"+urlPathSegment(req.TransactionHID), payload)
	if err != nil {
		return nil, err
	}
	resource, err := singleResource(op, resp)
	if err != nil {
		return nil, err
	}
	transaction, err := decodeTransaction(resource, c.cfg.NaiveTimeLocation)
	if err != nil {
		return nil, unknownResponseOutcome(op, resp, err)
	}
	return &transaction, nil
}
