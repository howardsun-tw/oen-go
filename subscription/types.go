package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	oen "github.com/howardsun-tw/oen-go"
)

// Status is a subscription state. A value this SDK does not know is passed
// through unchanged.
type Status string

const (
	StatusTrialing   Status = "trialing"   // 試用中
	StatusActive     Status = "active"     // 正常扣款中
	StatusPaused     Status = "paused"     // 扣款失敗，暫停中；NextChargeAt is the next retry
	StatusFailed     Status = "failed"     // 扣款失敗且不再重試
	StatusCancelled  Status = "cancelled"  // 已取消（期末取消）
	StatusTerminated Status = "terminated" // 已終止
)

// Known reports whether the status is one Oen documents.
func (s Status) Known() bool {
	switch s {
	case StatusTrialing, StatusActive, StatusPaused, StatusFailed, StatusCancelled, StatusTerminated:
		return true
	default:
		return false
	}
}

// Subscription is a subscription as Oen returned it. ID, Status and
// NextChargeAt are the documented fields; Raw holds the whole sanitized
// object, including the product, plan, customer, payments and history that
// GetSubscription returns in an undocumented shape.
type Subscription struct {
	ID     string
	Status Status
	// NextChargeAt is when Oen will charge next, always 09:00 Taipei time.
	// It is nil when the response does not carry it.
	NextChargeAt *time.Time
	Raw          json.RawMessage
}

// Product is a subscription product (prod_…). Its response fields are not
// documented, so only ID is typed.
type Product struct {
	ID  string
	Raw json.RawMessage
}

// Plan is a plan of a product (plan_…). Its response fields are not
// documented, so only ID is typed.
type Plan struct {
	ID  string
	Raw json.RawMessage
}

// Customer is a subscriber (cus_…). Its response fields are not documented,
// so only ID is typed.
type Customer struct {
	ID  string
	Raw json.RawMessage
}

// WebhookDelivery is one webhook delivery record. Its response fields are
// not documented, so only ID is typed.
type WebhookDelivery struct {
	ID  string
	Raw json.RawMessage
}

// Result is the sanitized data of a response whose shape Oen does not
// document, such as a termination with its refund estimate or a recovery
// link.
type Result struct {
	Data json.RawMessage
}

// Page is one page of a list. Oen returns 50 rows per page.
type Page[T any] struct {
	Items []T
	// Next is the token for the following page, empty on the last one. Pass
	// it back unchanged.
	Next string
}

// resourceFields are the fields read from any object. Each is optional; a
// field that is present with the wrong type is an error.
type resourceFields struct {
	ID           json.RawMessage `json:"id"`
	Status       json.RawMessage `json:"status"`
	NextChargeAt json.RawMessage `json:"nextChargeAt"`
}

func decodeFields(raw json.RawMessage) (resourceFields, json.RawMessage, error) {
	var fields resourceFields
	if err := decodeObject(raw, &fields); err != nil {
		return resourceFields{}, nil, err
	}
	clean, err := oen.SanitizeJSON(raw)
	if err != nil {
		return resourceFields{}, nil, err
	}
	return fields, clean, nil
}

func decodeID(raw json.RawMessage) (string, json.RawMessage, error) {
	fields, clean, err := decodeFields(raw)
	if err != nil {
		return "", nil, err
	}
	id, err := optionalString("id", fields.ID)
	return id, clean, err
}

func decodeSubscription(raw json.RawMessage) (Subscription, error) {
	fields, clean, err := decodeFields(raw)
	if err != nil {
		return Subscription{}, err
	}
	id, err := optionalString("id", fields.ID)
	if err != nil {
		return Subscription{}, err
	}
	status, err := optionalString("status", fields.Status)
	if err != nil {
		return Subscription{}, err
	}
	next, err := optionalTime("nextChargeAt", fields.NextChargeAt)
	if err != nil {
		return Subscription{}, err
	}
	return Subscription{ID: id, Status: Status(status), NextChargeAt: next, Raw: clean}, nil
}

func decodeProduct(raw json.RawMessage) (Product, error) {
	id, clean, err := decodeID(raw)
	return Product{ID: id, Raw: clean}, err
}

func decodePlan(raw json.RawMessage) (Plan, error) {
	id, clean, err := decodeID(raw)
	return Plan{ID: id, Raw: clean}, err
}

func decodeCustomer(raw json.RawMessage) (Customer, error) {
	id, clean, err := decodeID(raw)
	return Customer{ID: id, Raw: clean}, err
}

func decodeDelivery(raw json.RawMessage) (WebhookDelivery, error) {
	id, clean, err := decodeID(raw)
	return WebhookDelivery{ID: id, Raw: clean}, err
}

func decodeResult(raw json.RawMessage) (Result, error) {
	if isNull(raw) {
		return Result{}, nil
	}
	clean, err := oen.SanitizeJSON(raw)
	if err != nil {
		return Result{}, err
	}
	return Result{Data: clean}, nil
}

// decodePage reads a list response: data is an array and paging.next is a
// string, null or absent.
func decodePage[T any](body successBody, decode func(json.RawMessage) (T, error)) (*Page[T], error) {
	var rows []json.RawMessage
	trimmed := bytes.TrimSpace(body.Data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("list data is not an array")
	}
	if err := json.Unmarshal(trimmed, &rows); err != nil {
		return nil, fmt.Errorf("decode list data: %w", err)
	}
	page := &Page[T]{Items: make([]T, 0, len(rows))}
	for i, row := range rows {
		item, err := decode(row)
		if err != nil {
			return nil, fmt.Errorf("decode list row %d: %w", i, err)
		}
		page.Items = append(page.Items, item)
	}
	if isNull(body.Paging) {
		return page, nil
	}
	var paging struct {
		Next json.RawMessage `json:"next"`
	}
	if err := decodeObject(body.Paging, &paging); err != nil {
		return nil, fmt.Errorf("decode paging: %w", err)
	}
	next, err := optionalString("paging.next", paging.Next)
	if err != nil {
		return nil, err
	}
	page.Next = next
	return page, nil
}

func optionalString(field string, raw json.RawMessage) (string, error) {
	if isNull(raw) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", fmt.Errorf("%s must be a string", field)
	}
	return text, nil
}

// optionalTime reads a timestamp. Oen documents nextChargeAt's meaning but
// not its format, so an RFC 3339 string or a JSON number of Unix seconds
// (the format of the webhook's created field) is accepted. Anything else is
// an error, never a silent zero.
func optionalTime(field string, raw json.RawMessage) (*time.Time, error) {
	if isNull(raw) {
		return nil, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("%s is not a string", field)
		}
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		when, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text))
		if err != nil {
			return nil, fmt.Errorf("%s %q is not an RFC 3339 time", field, text)
		}
		return &when, nil
	}
	seconds, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s %s is neither an RFC 3339 string nor whole Unix seconds", field, trimmed)
	}
	when := time.Unix(seconds, 0).UTC()
	return &when, nil
}

func isNull(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}
