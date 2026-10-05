package subscription

import (
	"context"
	"net/http"
)

// KeyedRequest targets one subscription with an operation Oen requires an
// Idempotency-Key for.
//
// Keep IdempotencyKey until the outcome is known. Oen documents that a
// resend with the same key returns the original result, and that the same
// key with a different body is refused with SA044. Oen does not document the
// body fields of these operations, so Fields is sent as the JSON body; nil
// sends an empty object.
type KeyedRequest struct {
	SubscriptionID string
	IdempotencyKey string
	Fields         map[string]any
}

// ListProductSubscriptions reads one page of a product's subscriptions.
func (c *Client) ListProductSubscriptions(ctx context.Context, productID, page string) (*Page[Subscription], error) {
	const op = "ListProductSubscriptions"
	id, err := pathSegment(op, "productId", productID)
	if err != nil {
		return nil, err
	}
	return list(ctx, c, request{
		op: op, method: http.MethodGet,
		path: "/v1/products/" + id + "/subscriptions", query: pageQuery(page),
	}, decodeSubscription)
}

// GetSubscription reads one subscription with its product, plan, customer,
// payments and latest 100 history entries. Only ID, Status and NextChargeAt
// are typed; the rest is in Raw.
func (c *Client) GetSubscription(ctx context.Context, subscriptionID string) (*Subscription, error) {
	const op = "GetSubscription"
	id, err := pathSegment(op, "subscriptionId", subscriptionID)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodGet, path: "/v1/subscriptions/" + id}, decodeSubscription)
}

// ChangePlan moves a subscription to another plan (變更方案). An upgrade on a
// tiered product returns a payment link for the difference. Oen allows one
// successful plan change per subscription per Taipei day (SA013).
func (c *Client) ChangePlan(ctx context.Context, req KeyedRequest) (*Result, error) {
	const op = "ChangePlan"
	if err := requireFields(op, req.Fields); err != nil {
		return nil, err
	}
	return c.subscriptionWrite(ctx, op, "plan-change", req)
}

// ChangePeriod moves the end of the current period (變更期間結束日). The new
// end must be later than today (SA019).
func (c *Client) ChangePeriod(ctx context.Context, req KeyedRequest) (*Result, error) {
	const op = "ChangePeriod"
	if err := requireFields(op, req.Fields); err != nil {
		return nil, err
	}
	return c.subscriptionWrite(ctx, op, "period-change", req)
}

// CancelSubscription cancels at the end of the current period (期末取消).
func (c *Client) CancelSubscription(ctx context.Context, req KeyedRequest) (*Result, error) {
	return c.subscriptionWrite(ctx, "CancelSubscription", "cancel", req)
}

// ResumeSubscription resumes a cancelled subscription (恢復).
func (c *Client) ResumeSubscription(ctx context.Context, req KeyedRequest) (*Result, error) {
	return c.subscriptionWrite(ctx, "ResumeSubscription", "resume", req)
}

// TerminateSubscription ends a subscription now (立即終止). The response
// carries a prorated refund estimate; refund it with RefundSubscription.
func (c *Client) TerminateSubscription(ctx context.Context, req KeyedRequest) (*Result, error) {
	return c.subscriptionWrite(ctx, "TerminateSubscription", "terminate", req)
}

// RefundSubscription refunds a terminated subscription (終止後退款).
//
// SA028 (undetermined) and transport failures: resend with the same key.
// SA025 (in progress): query, do not resend. SA026 (that key's refund failed)
// and SA027 (processor refused): confirm the transaction, then use a new key.
func (c *Client) RefundSubscription(ctx context.Context, req KeyedRequest) (*Result, error) {
	return c.subscriptionWrite(ctx, "RefundSubscription", "refunds", req)
}

// CreateRecoveryLink creates a payment link for a subscription whose charge
// failed and is scheduled for retry (補繳連結). The link is valid for 3 days.
// It takes no Idempotency-Key.
func (c *Client) CreateRecoveryLink(ctx context.Context, subscriptionID string) (*Result, error) {
	const op = "CreateRecoveryLink"
	id, err := pathSegment(op, "subscriptionId", subscriptionID)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{
		op: op, method: http.MethodPost,
		path: "/v1/subscriptions/" + id + "/recovery-links", body: map[string]any{},
	}, decodeResult)
}

func (c *Client) subscriptionWrite(ctx context.Context, op, action string, req KeyedRequest) (*Result, error) {
	id, err := pathSegment(op, "subscriptionId", req.SubscriptionID)
	if err != nil {
		return nil, err
	}
	return c.keyedWrite(ctx, op, "/v1/subscriptions/"+id+"/"+action, req.IdempotencyKey, req.Fields)
}
