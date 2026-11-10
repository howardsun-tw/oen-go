// Package httpx holds the transport and webhook signature rules shared by the
// Payment API client (package oen) and the Subscription API client (package
// subscription).
package httpx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WithoutRedirects returns a copy of client that stops at the first response.
// The supplied client keeps its transport, timeout and cookie jar unchanged.
// A redirect can replay a write, so callers classify a 3xx as an unknown
// outcome instead of following it.
func WithoutRedirects(client *http.Client) *http.Client {
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &copied
}

// ReadLimited reads at most limit bytes. It reads one byte past the limit so
// an oversized body is detected rather than silently truncated.
func ReadLimited(response *http.Response, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("response body exceeds %s", byteSize(limit))
	}
	return raw, nil
}

func byteSize(limit int64) string {
	if limit%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", limit>>20)
	}
	return fmt.Sprintf("%d bytes", limit)
}

// ParseRetryAfter reads a Retry-After header as nonnegative integer seconds or
// an HTTP date (RFC 9110). Missing, invalid or past values return zero.
func ParseRetryAfter(headers http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return 0
	}
	// A very large valid delay must not wrap to zero and trigger an
	// immediate retry. Saturate at the representable duration limit.
	const maxDelay = time.Duration(1<<63 - 1)
	seconds, err := strconv.ParseUint(value, 10, 64)
	switch {
	case err == nil:
		if seconds > uint64(maxDelay/time.Second) {
			return maxDelay
		}
		return time.Duration(seconds) * time.Second
	case errors.Is(err, strconv.ErrRange):
		return maxDelay
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := when.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

// CheckHTTPURL returns a problem description when value is not an absolute
// http or https URL, or "" when it is.
func CheckHTTPURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Sprintf("%q is not an absolute URL", value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Sprintf("%q must use http or https", value)
	}
	return ""
}

// CheckBaseURL is CheckHTTPURL for a host that endpoint paths are appended
// to. Literal query or fragment delimiters would swallow the appended path;
// escaped delimiters in a path prefix are valid and remain untouched.
func CheckBaseURL(value string) string {
	if problem := CheckHTTPURL(value); problem != "" {
		return problem
	}
	if strings.ContainsAny(value, "?#") {
		return "base URL must not contain a query string or fragment"
	}
	return ""
}

// PrintableASCII reports whether value holds only bytes 0x20 to 0x7e. Use it
// for credentials and keys sent in headers.
func PrintableASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// CheckWebhookSignature checks an OenPay-Signature value,
// "t=<unix seconds>,v1=<hex>[,v1=<hex>]", against the raw body. Both APIs sign
// "<t>.<body>" with HMAC-SHA256 under the signing secret; t must be within
// tolerance of now in either direction, and any matching v1 passes. It returns
// a problem description, or "" when the signature is valid. The caller must
// refuse an empty secret first: anyone can compute a signature under it.
func CheckWebhookSignature(secret string, payload []byte, signature string, now time.Time, tolerance time.Duration) string {
	var timestamp string
	var signatures [][]byte
	for _, part := range strings.Split(signature, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			if timestamp != "" {
				return "more than one timestamp"
			}
			timestamp = value
		case "v1":
			decoded, err := hex.DecodeString(value)
			if err == nil && len(decoded) == sha256.Size {
				signatures = append(signatures, decoded)
			}
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || timestamp == "" || timestamp[0] == '-' || timestamp[0] == '+' {
		return "missing or malformed timestamp"
	}
	if len(signatures) == 0 {
		return "no v1 signature"
	}
	age := now.Sub(time.Unix(seconds, 0))
	if age > tolerance || age < -tolerance {
		return fmt.Sprintf("timestamp is outside the %s tolerance", tolerance)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	for _, candidate := range signatures {
		if hmac.Equal(candidate, expected) {
			return ""
		}
	}
	return "no signature matches the body"
}

// HasControlChars reports a byte net/http refuses in a header value. Such a
// value fails at send time as a transport error, which callers would read as
// an unknown outcome for a request that never left the process, so it must be
// refused when the client is configured.
func HasControlChars(value string) bool {
	for i := 0; i < len(value); i++ {
		if (value[i] < 0x20 && value[i] != '\t') || value[i] == 0x7f {
			return true
		}
	}
	return false
}
