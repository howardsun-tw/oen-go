package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/howardsun-tw/oen-go/internal/httpx"
)

// call performs one request and decodes its data. A decode failure after a
// successful response is an unknown outcome: the write may have applied.
func call[T any](ctx context.Context, c *Client, req request, decode func(json.RawMessage) (T, error)) (*T, error) {
	body, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	value, err := decode(body.Data)
	if err != nil {
		return nil, decodeFailure(req, body, err)
	}
	return &value, nil
}

func list[T any](ctx context.Context, c *Client, req request, decode func(json.RawMessage) (T, error)) (*Page[T], error) {
	body, err := c.do(ctx, req)
	if err != nil {
		return nil, err
	}
	page, err := decodePage(body, decode)
	if err != nil {
		return nil, decodeFailure(req, body, err)
	}
	return page, nil
}

func decodeFailure(req request, body successBody, err error) *Error {
	return &Error{
		Op:             req.op,
		Kind:           KindUnknownOutcome,
		HTTPStatus:     body.status,
		IdempotencyKey: req.idempotencyKey,
		Err:            fmt.Errorf("decode response data: %w", err),
	}
}

// keyedWrite sends one of the operations Oen requires an Idempotency-Key
// for. A nil fields map sends an empty JSON object.
func (c *Client) keyedWrite(ctx context.Context, op, path, key string, fields map[string]any) (*Result, error) {
	if err := validateIdempotencyKey(op, key); err != nil {
		return nil, err
	}
	return call(ctx, c, request{
		op: op, method: http.MethodPost, path: path,
		body: bodyOf(fields), idempotencyKey: key,
	}, decodeResult)
}

func bodyOf(fields map[string]any) map[string]any {
	if fields == nil {
		return map[string]any{}
	}
	return fields
}

// validateIdempotencyKey rejects a missing key locally, which Oen would
// refuse with SA034, and values that cannot travel in an HTTP header.
func validateIdempotencyKey(op, key string) error {
	if strings.TrimSpace(key) == "" {
		return newValidationError(op, "idempotencyKey", "Idempotency-Key is required for this operation")
	}
	if key != strings.TrimSpace(key) {
		return newValidationError(op, "idempotencyKey", "Idempotency-Key must not start or end with whitespace")
	}
	if !httpx.PrintableASCII(key) {
		return newValidationError(op, "idempotencyKey", "Idempotency-Key must be printable ASCII")
	}
	return nil
}

func requireFields(op string, fields map[string]any) error {
	if len(fields) == 0 {
		return newValidationError(op, "fields", "at least one field is required")
	}
	return nil
}

func maxRunes(op, field, value string, limit int) error {
	if utf8.RuneCountInString(value) > limit {
		return newValidationError(op, field, fmt.Sprintf("%s must be at most %d characters", field, limit))
	}
	return nil
}

func httpsURL(op, field, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return newValidationError(op, field, fmt.Sprintf("%s must be an absolute https URL", field))
	}
	return nil
}
