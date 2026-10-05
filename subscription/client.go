package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/howardsun-tw/oen-go/internal/httpx"
)

// maxResponseBytes bounds one response body. The largest documented response
// is a list page of 50 rows; a subscription carries up to 100 history rows.
const maxResponseBytes = 4 << 20

// Client talks to the Subscription API for one merchant. It is safe for
// concurrent use and never retries a request.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// New validates the configuration and returns a client. It performs no I/O.
func New(cfg Config) (*Client, error) {
	normalized, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	// A redirect can replay a write, so the SDK stops at the first response
	// and classifies a redirect as an unknown outcome. Oen's own webhook
	// delivery does not follow redirects either.
	return &Client{cfg: normalized, httpClient: httpx.WithoutRedirects(normalized.HTTPClient)}, nil
}

// BaseURL returns the API host in use.
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

// successBody is Oen's success shape: {"data": …, "paging"?: {"next": …}}.
type successBody struct {
	Data   json.RawMessage `json:"data"`
	Paging json.RawMessage `json:"paging"`
	status int
}

// errorBody is Oen's error shape: {"errno": "SA001", "message": "…"}.
type errorBody struct {
	Errno   string `json:"errno"`
	Message string `json:"message"`
}

type request struct {
	op             string
	method         string
	path           string
	query          url.Values
	body           any
	idempotencyKey string
}

// do performs exactly one HTTP request. It never sends a second one.
func (c *Client) do(ctx context.Context, req request) (successBody, error) {
	if ctx == nil {
		return successBody{}, newValidationError(req.op, "ctx", "context must not be nil")
	}
	var payload []byte
	if req.body != nil {
		encoded, err := json.Marshal(req.body)
		if err != nil {
			return successBody{}, newValidationError(req.op, "body", fmt.Sprintf("encode request: %v", err))
		}
		payload = encoded
	}

	callCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	target := c.cfg.BaseURL + req.path
	if len(req.query) > 0 {
		target += "?" + req.query.Encode()
	}
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	httpRequest, err := http.NewRequestWithContext(callCtx, req.method, target, bodyReader)
	if err != nil {
		return successBody{}, newValidationError(req.op, "request", fmt.Sprintf("build request: %v", err))
	}
	// net/http replays a request on a fresh connection when a reused one
	// fails, if the request has a rewindable body and an Idempotency-Key
	// header (Request.isReplayable). That replay would be a second request
	// the caller never decided to send. Without GetBody the body cannot be
	// rewound, so the transport reports the failure instead.
	httpRequest.GetBody = nil
	httpRequest.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	httpRequest.Header.Set("Accept", "application/json")
	if payload != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if req.idempotencyKey != "" {
		httpRequest.Header.Set("Idempotency-Key", req.idempotencyKey)
	}
	if c.cfg.UserAgent != "" {
		httpRequest.Header.Set("User-Agent", c.cfg.UserAgent)
	}

	started := c.cfg.Now()
	httpResponse, err := c.httpClient.Do(httpRequest)
	duration := c.cfg.Now().Sub(started)
	if err != nil {
		// The request may have reached Oen before the failure.
		failure := &Error{Op: req.op, Kind: KindUnknownOutcome, Err: fmt.Errorf("transport failure: %w", err)}
		return successBody{}, c.finish(req, 0, failure, duration)
	}
	defer func() { _ = httpResponse.Body.Close() }()

	raw, readErr := httpx.ReadLimited(httpResponse, maxResponseBytes)
	if readErr != nil {
		failure := &Error{Op: req.op, Kind: KindUnknownOutcome, HTTPStatus: httpResponse.StatusCode, Err: readErr}
		if httpResponse.StatusCode == http.StatusTooManyRequests {
			// The headers still carry the rate-limit signal.
			failure = classify(req.op, req.method, httpResponse.StatusCode, httpResponse.Header, nil, readErr, c.cfg.Now())
		}
		return successBody{}, c.finish(req, httpResponse.StatusCode, failure, duration)
	}

	status := httpResponse.StatusCode
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		var success successBody
		decodeErr := decodeObject(raw, &success)
		if decodeErr == nil && len(success.Data) == 0 {
			// Oen documents every success as {"data": …}. A body without the
			// key is not a confirmation that the request applied; an explicit
			// "data": null still is.
			decodeErr = errors.New(`response carries no "data" field`)
		}
		if failure := classify(req.op, req.method, status, httpResponse.Header, nil, decodeErr, c.cfg.Now()); failure != nil {
			return successBody{}, c.finish(req, status, failure, duration)
		}
		c.log(req, status, "success", duration)
		success.status = status
		return success, nil
	}
	var provider errorBody
	decodeErr := decodeObject(raw, &provider)
	var body *errorBody
	if decodeErr == nil {
		body = &provider
	}
	failure := classify(req.op, req.method, status, httpResponse.Header, body, decodeErr, c.cfg.Now())
	return successBody{}, c.finish(req, status, failure, duration)
}

func (c *Client) finish(req request, status int, failure *Error, duration time.Duration) *Error {
	failure.IdempotencyKey = req.idempotencyKey
	c.log(req, status, failure.Kind, duration)
	return failure
}

func (c *Client) log(req request, status int, outcome any, duration time.Duration) {
	c.cfg.Logf("oen subscription: method=%s path=%s status=%d outcome=%v duration=%s",
		req.method, req.path, status, outcome, duration)
}

// decodeObject requires a single JSON object.
func decodeObject(raw []byte, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("response body is not a JSON object")
	}
	return json.Unmarshal(trimmed, target)
}

// pathSegment escapes one identifier and refuses values that would change
// the endpoint, such as "." or "..".
func pathSegment(op, field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", newValidationError(op, field, field+" is required")
	}
	if value == "." || value == ".." {
		return "", newValidationError(op, field, field+" is not a valid identifier")
	}
	return url.PathEscape(value), nil
}

func pageQuery(page string) url.Values {
	if page == "" {
		return nil
	}
	// Oen asks for the previous paging.next value unchanged.
	return url.Values{"page": []string{page}}
}
