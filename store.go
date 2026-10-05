package oen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// StoreSubscriptionStatus is the state of an Oen store (應援商店) recurring
// order. It is a different set from [SubscriptionStatus].
type StoreSubscriptionStatus string

const (
	StoreSubscriptionOngoing        StoreSubscriptionStatus = "ongoing"        // 進行中
	StoreSubscriptionRetryScheduled StoreSubscriptionStatus = "retryScheduled" // 待重扣
	StoreSubscriptionError          StoreSubscriptionStatus = "error"          // 扣款失敗
	StoreSubscriptionCancelled      StoreSubscriptionStatus = "cancelled"      // 已取消
	StoreSubscriptionDone           StoreSubscriptionStatus = "done"           // 已完成
)

// Known reports whether the status is one Oen documents.
func (s StoreSubscriptionStatus) Known() bool {
	switch s {
	case StoreSubscriptionOngoing, StoreSubscriptionRetryScheduled, StoreSubscriptionError,
		StoreSubscriptionCancelled, StoreSubscriptionDone:
		return true
	default:
		return false
	}
}

// ListStoreSubscriptionsRequest filters the store's recurring orders
// (查詢商店定期購訂單列表).
type ListStoreSubscriptionsRequest struct {
	// Statuses limits the list; empty lists every status. With a filter a
	// page may hold fewer than 50 rows and still have a next page.
	Statuses []StoreSubscriptionStatus
	// Page is the previous response's NextPage, passed back unchanged.
	Page string
}

// StoreSubscription is one recurring order sold through the Oen store. It is
// not a Payment API subscription; use [Client.GetSubscription] for those.
// Every field is optional in Oen's documentation.
type StoreSubscription struct {
	ID     string
	Status StoreSubscriptionStatus
	// TotalAmount is the amount charged each period. HasTotalAmount tells an
	// omitted value from zero.
	TotalAmount    Amount
	HasTotalAmount bool
	// Period is the number of periods charged so far, NumberOfPeriods the
	// total.
	Period          int
	NumberOfPeriods int
	// Items is the ordered goods. Oen documents it as an array without
	// describing its elements, so it is the sanitized JSON array.
	Items json.RawMessage
	// Customer carries user.name and user.email; ID is always empty.
	Customer Customer

	CreatedAt     *time.Time
	LastChargedAt *time.Time
	CancelledAt   *time.Time
	EndedAt       *time.Time

	RedactedPayload json.RawMessage
}

// StoreSubscriptionPage is one page of store recurring orders, newest first.
type StoreSubscriptionPage struct {
	Subscriptions []StoreSubscription
	// NextPage is the token for the following page, empty on the last one.
	NextPage string
}

// ListStoreSubscriptions returns one page of the recurring orders sold
// through the Oen store (GET /subscriptions). Only merchants that sell
// subscription goods in the Oen store need it.
func (c *Client) ListStoreSubscriptions(ctx context.Context, req ListStoreSubscriptionsRequest) (*StoreSubscriptionPage, error) {
	const op = "ListStoreSubscriptions"
	statuses := make([]string, 0, len(req.Statuses))
	for _, status := range req.Statuses {
		if !status.Known() {
			return nil, newValidationError(op, "status", fmt.Sprintf("unknown store subscription status %q", status))
		}
		statuses = append(statuses, string(status))
	}
	query := url.Values{}
	if len(statuses) > 0 {
		query.Set("status", strings.Join(statuses, ","))
	}
	if req.Page != "" {
		query.Set("page", req.Page)
	}
	path := "/subscriptions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.get(ctx, op, path)
	if err != nil {
		return nil, err
	}
	page, err := decodeStoreSubscriptionPage(resp.env.Data, c.cfg.NaiveTimeLocation)
	if err != nil {
		return nil, unknownResponseOutcome(op, resp, err)
	}
	return page, nil
}

// decodeStoreSubscriptionPage reads {"subscriptions": [...], "page": ...}.
// Oen documents both keys as always present. A missing or non-array
// subscriptions is an error rather than an empty list; a missing or null page
// ends pagination.
func decodeStoreSubscriptionPage(data json.RawMessage, loc *time.Location) (*StoreSubscriptionPage, error) {
	if isNullRaw(data) {
		return nil, errNoField("data")
	}
	var wire struct {
		Subscriptions json.RawMessage `json:"subscriptions"`
		Page          json.RawMessage `json:"page"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("decode store subscription list: %w", err)
	}
	if isNullRaw(wire.Subscriptions) {
		return nil, errNoField("data.subscriptions")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(wire.Subscriptions, &rows); err != nil {
		return nil, fmt.Errorf("store subscription list is not an array: %w", err)
	}
	page := &StoreSubscriptionPage{Subscriptions: make([]StoreSubscription, 0, len(rows))}
	for i, row := range rows {
		subscription, err := decodeStoreSubscription(row, loc)
		if err != nil {
			return nil, fmt.Errorf("store subscription %d: %w", i, err)
		}
		page.Subscriptions = append(page.Subscriptions, subscription)
	}
	if !isNullRaw(wire.Page) {
		if err := json.Unmarshal(wire.Page, &page.NextPage); err != nil {
			return nil, fmt.Errorf("decode store subscription page token: %w", err)
		}
	}
	return page, nil
}

func decodeStoreSubscription(resource json.RawMessage, loc *time.Location) (StoreSubscription, error) {
	clean, err := SanitizeJSON(resource)
	if err != nil {
		return StoreSubscription{}, fmt.Errorf("sanitize: %w", err)
	}
	var wire struct {
		ID              json.RawMessage `json:"id"`
		Status          json.RawMessage `json:"status"`
		Items           json.RawMessage `json:"items"`
		TotalAmount     json.RawMessage `json:"totalAmount"`
		Period          json.RawMessage `json:"period"`
		NumberOfPeriods json.RawMessage `json:"numberOfPeriods"`
		LastChargedAt   json.RawMessage `json:"lastChargedAt"`
		User            *struct {
			Name  json.RawMessage `json:"name"`
			Email json.RawMessage `json:"email"`
		} `json:"user"`
		CreatedAt   json.RawMessage `json:"createdAt"`
		CancelledAt json.RawMessage `json:"cancelledAt"`
		EndedAt     json.RawMessage `json:"endedAt"`
	}
	if err := json.Unmarshal(clean, &wire); err != nil {
		return StoreSubscription{}, fmt.Errorf("decode: %w", err)
	}
	subscription := StoreSubscription{
		ID:              rawString(wire.ID),
		Status:          StoreSubscriptionStatus(rawString(wire.Status)),
		HasTotalAmount:  !isNullRaw(wire.TotalAmount),
		RedactedPayload: clean,
	}
	if subscription.TotalAmount, err = parseOptionalAmount(wire.TotalAmount); err != nil {
		return StoreSubscription{}, fmt.Errorf("totalAmount: %w", err)
	}
	if subscription.Period, err = parseOptionalInt(wire.Period); err != nil {
		return StoreSubscription{}, fmt.Errorf("period: %w", err)
	}
	if subscription.NumberOfPeriods, err = parseOptionalInt(wire.NumberOfPeriods); err != nil {
		return StoreSubscription{}, fmt.Errorf("numberOfPeriods: %w", err)
	}
	if !isNullRaw(wire.Items) {
		trimmed := strings.TrimSpace(string(wire.Items))
		if !strings.HasPrefix(trimmed, "[") {
			return StoreSubscription{}, fmt.Errorf("items is not an array")
		}
		subscription.Items = json.RawMessage(trimmed)
	}
	if wire.User != nil {
		subscription.Customer = Customer{Name: rawString(wire.User.Name), Email: rawString(wire.User.Email)}
	}
	for _, field := range []struct {
		name   string
		raw    json.RawMessage
		target **time.Time
	}{
		{"createdAt", wire.CreatedAt, &subscription.CreatedAt},
		{"lastChargedAt", wire.LastChargedAt, &subscription.LastChargedAt},
		{"cancelledAt", wire.CancelledAt, &subscription.CancelledAt},
		{"endedAt", wire.EndedAt, &subscription.EndedAt},
	} {
		when, err := optionalTime(field.raw, loc)
		if err != nil {
			return StoreSubscription{}, fmt.Errorf("%s: %w", field.name, err)
		}
		*field.target = when
	}
	return subscription, nil
}
