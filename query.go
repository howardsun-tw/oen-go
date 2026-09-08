package oen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ListTransactionsRequest filters the merchant's transaction list
// (查詢交易列表). Oen returns 50 rows per page and pages with a token rather
// than an offset.
type ListTransactionsRequest struct {
	// Start and End bound the period. Oen documents its date fields as ISO
	// dates in UTC+0 and does not document further constraints on this query.
	Start string
	End   string
	// Page is the token from a previous response's NextPage.
	Page string
}

// TransactionPage is one page of the transaction list.
type TransactionPage struct {
	Transactions []Transaction
	// NextPage is the token for the following page, empty on the last one.
	NextPage string
}

// CancelSubscriptionRequest stops a recurring charge (取消定期定額).
type CancelSubscriptionRequest struct {
	SubscriptionID string
	// Reason is stored by Oen and shown in its console.
	Reason string
}

// GetTransaction reads one transaction (查詢交易明細). It accepts either
// identifier Oen exposes: the transaction HID or the transaction ID.
func (c *Client) GetTransaction(ctx context.Context, transactionID string) (*Transaction, error) {
	const op = "GetTransaction"
	if strings.TrimSpace(transactionID) == "" {
		return nil, newValidationError(op, "id", "transaction ID is required")
	}
	resp, err := c.get(ctx, op, "/transactions/"+urlPathSegment(transactionID))
	if err != nil {
		return nil, err
	}
	resource, err := singleResource(op, resp, "transaction")
	if err != nil {
		return nil, err
	}
	transaction, err := decodeTransaction(resource, c.cfg.NaiveTimeLocation)
	if err != nil {
		return nil, unknownResponseOutcome(op, resp, err)
	}
	return &transaction, nil
}

// ListOrderTransactions returns every transaction Oen holds for one merchant
// order reference (用訂單編號查詢交易列表).
//
// Use this query to reconcile an [ErrUnknownOutcome] charge. An empty result
// does not prove that an earlier request will never finish; the caller owns
// reconciliation and any decision to resend.
func (c *Client) ListOrderTransactions(ctx context.Context, orderID string) ([]Transaction, error) {
	const op = "ListOrderTransactions"
	if strings.TrimSpace(orderID) == "" {
		return nil, newValidationError(op, "orderId", "order ID is required")
	}
	resp, err := c.get(ctx, op, "/order/"+urlPathSegment(orderID)+"/transactions")
	if err != nil {
		return nil, err
	}
	page, err := c.decodeTransactionPage(op, resp)
	if err != nil {
		return nil, err
	}
	return page.Transactions, nil
}

// ListTransactions returns one page of the merchant's transactions
// (查詢交易列表).
func (c *Client) ListTransactions(ctx context.Context, req ListTransactionsRequest) (*TransactionPage, error) {
	const op = "ListTransactions"
	query := url.Values{}
	for key, value := range map[string]string{"page": req.Page, "start": req.Start, "end": req.End} {
		if value != "" {
			query.Set(key, value)
		}
	}
	path := "/transactions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.get(ctx, op, path)
	if err != nil {
		return nil, err
	}
	return c.decodeTransactionPage(op, resp)
}

// GetSubscription reads one subscription (查詢定期定額明細).
func (c *Client) GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error) {
	const op = "GetSubscription"
	if strings.TrimSpace(subscriptionID) == "" {
		return nil, newValidationError(op, "subscriptionId", "subscription ID is required")
	}
	resp, err := c.get(ctx, op, "/subscriptions/"+urlPathSegment(subscriptionID))
	if err != nil {
		return nil, err
	}
	return c.subscriptionFrom(op, resp, true)
}

// CancelSubscription stops a recurring charge. Oen answers with the
// subscription in its cancelled state, possibly without amount. Check
// Subscription.HasAmount before using the returned Amount.
func (c *Client) CancelSubscription(ctx context.Context, req CancelSubscriptionRequest) (*Subscription, error) {
	const op = "CancelSubscription"
	if strings.TrimSpace(req.SubscriptionID) == "" {
		return nil, newValidationError(op, "subscriptionId", "subscription ID is required")
	}
	payload := map[string]any{"merchantId": c.cfg.MerchantID}
	addOptional(payload, "reason", req.Reason)

	resp, err := c.put(ctx, op, "/subscriptions/"+urlPathSegment(req.SubscriptionID), payload)
	if err != nil {
		return nil, err
	}
	return c.subscriptionFrom(op, resp, false)
}

func (c *Client) subscriptionFrom(op string, resp response, requireAmount bool) (*Subscription, error) {
	resource, err := singleResource(op, resp, "subscription")
	if err != nil {
		return nil, err
	}
	subscription, err := decodeSubscription(resource, c.cfg.NaiveTimeLocation, requireAmount)
	if err != nil {
		return nil, unknownResponseOutcome(op, resp, err)
	}
	return &subscription, nil
}

func (c *Client) decodeTransactionPage(op string, resp response) (*TransactionPage, error) {
	resources, nextPage, err := listResources(resp.env.Data)
	if err != nil {
		return nil, unknownResponseOutcome(op, resp, err)
	}
	transactions := make([]Transaction, 0, len(resources))
	for _, resource := range resources {
		transaction, err := decodeTransaction(resource, c.cfg.NaiveTimeLocation)
		if err != nil {
			return nil, unknownResponseOutcome(op, resp, err)
		}
		transactions = append(transactions, transaction)
	}
	return &TransactionPage{Transactions: transactions, NextPage: nextPage}, nil
}

// singleResource unwraps the resource Oen puts in data. Every documented
// endpoint returns the resource directly; the wrapper key is accepted only
// when the outer object is not itself the resource, so a nested object of
// another kind can never be mistaken for the one the caller asked for.
func singleResource(op string, resp response, wrapperKey string) (json.RawMessage, error) {
	data := resp.env.Data
	if isNullRaw(data) {
		return nil, unknownResponseOutcome(op, resp, errNoField("data"))
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, unknownResponseOutcome(op, resp, fmt.Errorf("decode data: %w", err))
	}
	if isNullRaw(object["id"]) {
		if wrapped := object[wrapperKey]; !isNullRaw(wrapped) {
			return wrapped, nil
		}
	}
	return data, nil
}

// listResources reads a list of resources and its page token. An envelope
// shape the SDK cannot read is an error, never an empty list: an empty list
// would read as "the provider holds nothing", which is a different fact. The
// shapes that do mean "nothing": a bare empty array, a known list key that is
// empty or null, and an object carrying nothing but a page token. Oen's
// published examples show only non-empty lists, so the empty shapes are the
// SDK's assumption until a real empty response is captured.
func listResources(data json.RawMessage) ([]json.RawMessage, string, error) {
	if isNullRaw(data) {
		return nil, "", errNoField("data")
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return list, "", nil
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, "", fmt.Errorf("decode transaction list: %w", err)
	}
	var page string
	if raw := wrapper["page"]; !isNullRaw(raw) {
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, "", fmt.Errorf("decode transaction page token: %w", err)
		}
	}
	for _, key := range []string{"transactions", "items", "data"} {
		raw, present := wrapper[key]
		if !present {
			continue
		}
		if isNullRaw(raw) {
			return []json.RawMessage{}, page, nil
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, "", fmt.Errorf("decode transaction list %q: %w", key, err)
		}
		return list, page, nil
	}
	for key := range wrapper {
		if key != "page" {
			return nil, "", fmt.Errorf("transaction list is not an array")
		}
	}
	return []json.RawMessage{}, page, nil
}
