package subscription

import (
	"context"
	"net/http"
)

// UpdateCustomerRequest is PUT /v1/customers/{customerId}. Oen allows the
// name, phone and address to change, but not the email, and does not
// document the field names, so Fields is sent as the JSON body.
type UpdateCustomerRequest struct {
	CustomerID string
	Fields     map[string]any
}

// ListCustomers reads one page of customers.
func (c *Client) ListCustomers(ctx context.Context, page string) (*Page[Customer], error) {
	return list(ctx, c, request{op: "ListCustomers", method: http.MethodGet, path: "/v1/customers", query: pageQuery(page)}, decodeCustomer)
}

// GetCustomer reads one customer.
func (c *Client) GetCustomer(ctx context.Context, customerID string) (*Customer, error) {
	const op = "GetCustomer"
	id, err := pathSegment(op, "customerId", customerID)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodGet, path: "/v1/customers/" + id}, decodeCustomer)
}

// UpdateCustomer changes a customer's name, phone or address.
func (c *Client) UpdateCustomer(ctx context.Context, req UpdateCustomerRequest) (*Customer, error) {
	const op = "UpdateCustomer"
	id, err := pathSegment(op, "customerId", req.CustomerID)
	if err != nil {
		return nil, err
	}
	if err := requireFields(op, req.Fields); err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodPut, path: "/v1/customers/" + id, body: req.Fields}, decodeCustomer)
}

// ListWebhookDeliveries reads one page of webhook delivery records. It needs
// the ops:webhook scope.
func (c *Client) ListWebhookDeliveries(ctx context.Context, page string) (*Page[WebhookDelivery], error) {
	return list(ctx, c, request{
		op: "ListWebhookDeliveries", method: http.MethodGet,
		path: "/v1/webhook-deliveries", query: pageQuery(page),
	}, decodeDelivery)
}

// GetWebhookDelivery reads one webhook delivery record.
func (c *Client) GetWebhookDelivery(ctx context.Context, deliveryID string) (*WebhookDelivery, error) {
	const op = "GetWebhookDelivery"
	id, err := pathSegment(op, "deliveryId", deliveryID)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodGet, path: "/v1/webhook-deliveries/" + id}, decodeDelivery)
}

// ResendWebhookDelivery sends a delivery again. Oen requires an
// Idempotency-Key. The receiver may see the same event more than once and
// must deduplicate by event ID.
func (c *Client) ResendWebhookDelivery(ctx context.Context, deliveryID, idempotencyKey string) (*Result, error) {
	const op = "ResendWebhookDelivery"
	id, err := pathSegment(op, "deliveryId", deliveryID)
	if err != nil {
		return nil, err
	}
	return c.keyedWrite(ctx, op, "/v1/webhook-deliveries/"+id+"/resend", idempotencyKey, nil)
}
