package oen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxResponseBytes bounds one response body. Oen's largest documented
// response is a page of 50 transactions, far below this.
const maxResponseBytes = 1 << 20

// Client talks to one Oen merchant account. It is safe for concurrent use.
//
// No method retries. A request that changes state and does not return a clear
// answer produces an [ErrUnknownOutcome] error, and the caller decides what to
// do after querying the transaction.
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
	// Keep the caller's transport, timeout and cookie jar without mutating
	// their client. Redirects can replay a POST, so the SDK always stops at
	// the first response and classifies a redirect as an unknown outcome.
	httpClient := *normalized.HTTPClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{cfg: normalized, httpClient: &httpClient}, nil
}

// MerchantID returns the merchant this client acts for.
func (c *Client) MerchantID() string { return c.cfg.MerchantID }

// Environment returns the configured environment, empty when the caller set
// the hosts explicitly.
func (c *Client) Environment() Environment { return c.cfg.Environment }

// BaseURL returns the API host in use.
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

// CheckoutBaseURL returns the hosted-page host used to build redirect URLs.
func (c *Client) CheckoutBaseURL() string { return c.cfg.CheckoutBaseURL }

type response struct {
	status int
	body   []byte
	env    envelope
}

// send encodes payload as JSON and performs one request with the given
// method. Only POST and PUT carry a body.
func (c *Client) send(ctx context.Context, op, method, path string, payload any) (response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return response{}, &Error{Op: op, Kind: KindInvalidInput, Err: fmt.Errorf("encode request: %w", err)}
	}
	return c.do(ctx, op, method, path, body)
}

func (c *Client) post(ctx context.Context, op, path string, payload any) (response, error) {
	return c.send(ctx, op, http.MethodPost, path, payload)
}

func (c *Client) put(ctx context.Context, op, path string, payload any) (response, error) {
	return c.send(ctx, op, http.MethodPut, path, payload)
}

func (c *Client) get(ctx context.Context, op, path string) (response, error) {
	return c.do(ctx, op, http.MethodGet, path, nil)
}

// do performs exactly one HTTP request. It never sends a second one.
func (c *Client) do(ctx context.Context, op, method, path string, body []byte) (response, error) {
	if ctx == nil {
		return response{}, newValidationError(op, "ctx", "context must not be nil")
	}
	callCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	started := c.cfg.Now()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(callCtx, method, c.cfg.BaseURL+path, bodyReader)
	if err != nil {
		return response{}, &Error{Op: op, Kind: KindInvalidInput, Err: fmt.Errorf("build request: %w", err)}
	}
	request.Header.Set("Authorization", "Bearer "+c.cfg.AuthToken)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.UserAgent != "" {
		request.Header.Set("User-Agent", c.cfg.UserAgent)
	}

	httpResponse, err := c.httpClient.Do(request)
	duration := c.cfg.Now().Sub(started)
	if err != nil {
		// A transport failure may have happened after the request left the
		// process; the SDK cannot tell, so it proves nothing about whether
		// Oen applied it.
		c.log(method, path, 0, KindUnknownOutcome, duration)
		return response{}, unknownOutcome(op, fmt.Errorf("transport failure: %w", err))
	}
	defer func() { _ = httpResponse.Body.Close() }()

	raw, err := readLimited(httpResponse)
	if err != nil {
		failure := unknownResponseOutcome(op, response{status: httpResponse.StatusCode}, err)
		if httpResponse.StatusCode == http.StatusTooManyRequests {
			// The headers still provide a rate-limit signal and delay even if
			// the body is oversized, truncated or times out while being read.
			failure = classify(op, method, httpResponse.StatusCode, httpResponse.Header, nil, err, c.cfg.Now())
		}
		c.log(method, path, httpResponse.StatusCode, failure.Kind, duration)
		return response{}, failure
	}

	var env envelope
	decodeErr := json.Unmarshal(raw, &env)
	if classified := classify(op, method, httpResponse.StatusCode, httpResponse.Header, &env, decodeErr, c.cfg.Now()); classified != nil {
		c.log(method, path, httpResponse.StatusCode, classified.Kind, duration)
		return response{}, classified
	}
	c.log(method, path, httpResponse.StatusCode, "success", duration)
	return response{status: httpResponse.StatusCode, body: raw, env: env}, nil
}

// readLimited reads one byte past the accepted size so an oversized body is
// detected rather than silently truncated.
func readLimited(httpResponse *http.Response) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, errors.New("response body exceeds 1 MiB")
	}
	return raw, nil
}

func (c *Client) log(method, path string, status int, outcome any, duration time.Duration) {
	c.cfg.Logf("oen: method=%s path=%s status=%d outcome=%v duration=%s", method, path, status, outcome, duration)
}

// decodeData reads the envelope's data field into target.
func decodeData(op string, resp response, target any) error {
	if isNullRaw(resp.env.Data) {
		return unknownResponseOutcome(op, resp, errors.New("response carries no data object"))
	}
	if err := json.Unmarshal(resp.env.Data, target); err != nil {
		return unknownResponseOutcome(op, resp, fmt.Errorf("decode response data: %w", err))
	}
	return nil
}

func (c *Client) checkoutURL(path, id string) string {
	if c.cfg.CheckoutBaseURL == "" {
		return ""
	}
	return c.cfg.CheckoutBaseURL + path + urlPathSegment(id)
}
