package httpx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	tests := map[string]time.Duration{
		"":                              0,
		"5":                             5 * time.Second,
		" 5 ":                           5 * time.Second,
		"-1":                            0,
		"1.5":                           0,
		"soon":                          0,
		"Mon, 05 Oct 2026 00:00:30 GMT": 30 * time.Second,
		"Sun, 04 Oct 2026 00:00:00 GMT": 0,
		"99999999999999999999999":       time.Duration(1<<63 - 1),
		"9223372036854775807":           time.Duration(1<<63 - 1),
	}
	for value, want := range tests {
		header := http.Header{}
		if value != "" {
			header.Set("Retry-After", value)
		}
		if got := ParseRetryAfter(header, now); got != want {
			t.Fatalf("%q: want %s, got %s", value, want, got)
		}
	}
}

func TestReadLimited(t *testing.T) {
	read := func(body string, limit int64) ([]byte, error) {
		return ReadLimited(&http.Response{Body: io.NopCloser(strings.NewReader(body))}, limit)
	}
	raw, err := read("12345", 5)
	if err != nil || string(raw) != "12345" {
		t.Fatalf("at the limit: %q %v", raw, err)
	}
	if _, err := read("123456", 5); err == nil || !strings.Contains(err.Error(), "exceeds 5 bytes") {
		t.Fatalf("over the limit: %v", err)
	}
	if _, err := read(strings.Repeat("a", 1<<20+1), 1<<20); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("over 1 MiB: %v", err)
	}
}

func TestWithoutRedirectsCopiesTheClient(t *testing.T) {
	supplied := &http.Client{Timeout: time.Second}
	copied := WithoutRedirects(supplied)
	if supplied.CheckRedirect != nil {
		t.Fatal("supplied client was changed")
	}
	if copied.Timeout != time.Second {
		t.Fatal("timeout was not kept")
	}
	if err := copied.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("redirects are followed: %v", err)
	}
}

func TestCheckURLs(t *testing.T) {
	tests := []struct {
		value   string
		http    bool
		baseURL bool
	}{
		{"https://example.com", true, true},
		{"http://localhost:8080/prefix", true, true},
		{"https://example.com/a%3Fb", true, true},
		{"https://example.com/?x=1", true, false},
		{"https://example.com/#top", true, false},
		{"example.com", false, false},
		{"ftp://example.com", false, false},
		{"", false, false},
	}
	for _, tc := range tests {
		if got := CheckHTTPURL(tc.value) == ""; got != tc.http {
			t.Fatalf("CheckHTTPURL(%q) ok=%v", tc.value, got)
		}
		if got := CheckBaseURL(tc.value) == ""; got != tc.baseURL {
			t.Fatalf("CheckBaseURL(%q) ok=%v", tc.value, got)
		}
	}
}

func TestHeaderChecksMatchWhatNetHTTPSends(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	for _, value := range []string{"app/1.0", "a\tb", "tök", "a\nb", "a\rb", "a\x00b", "a\x7fb", "a\x1fb"} {
		request, err := http.NewRequest(http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("User-Agent", value)
		response, sendErr := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		if refused := sendErr != nil; refused != HasControlChars(value) {
			t.Fatalf("%q: net/http refused=%v, HasControlChars=%v", value, refused, HasControlChars(value))
		}
	}
	for value, want := range map[string]bool{"sub_sk_abc": true, "a b~": true, "a\tb": false, "tök": false, "a\x7f": false} {
		if got := PrintableASCII(value); got != want {
			t.Fatalf("PrintableASCII(%q) = %v", value, got)
		}
	}
}

func TestCheckWebhookSignature(t *testing.T) {
	const secret = "pay_whsec_0123456789abcdef"
	now := time.Unix(1790000000, 0)
	body := []byte(`{"purpose":"charge"}`)
	sign := func(key, timestamp string, payload []byte) string {
		mac := hmac.New(sha256.New, []byte(key))
		mac.Write([]byte(timestamp + "."))
		mac.Write(payload)
		return hex.EncodeToString(mac.Sum(nil))
	}
	good := sign(secret, "1790000000", body)
	rotated := sign("pay_whsec_old", "1790000000", body)

	for name, test := range map[string]struct{ signature, want string }{
		"valid":                {"t=1790000000,v1=" + good, ""},
		"spaces around parts":  {" t=1790000000 , v1=" + good, ""},
		"second v1 matches":    {"t=1790000000,v1=" + rotated + ",v1=" + good, ""},
		"edge of tolerance":    {"t=1789999700,v1=" + sign(secret, "1789999700", body), ""},
		"empty":                {"", "missing or malformed timestamp"},
		"signed timestamp":     {"t=+1790000000,v1=" + good, "missing or malformed timestamp"},
		"two timestamps":       {"t=1790000000,t=1790000001,v1=" + good, "more than one timestamp"},
		"no v1":                {"t=1790000000,v0=" + good, "no v1 signature"},
		"short v1":             {"t=1790000000,v1=" + good[:32], "no v1 signature"},
		"too old":              {"t=1789999699,v1=" + sign(secret, "1789999699", body), "timestamp is outside the 5m0s tolerance"},
		"too new":              {"t=1790000301,v1=" + sign(secret, "1790000301", body), "timestamp is outside the 5m0s tolerance"},
		"another secret":       {"t=1790000000,v1=" + rotated, "no signature matches the body"},
		"another body":         {"t=1790000000,v1=" + sign(secret, "1790000000", []byte(`{}`)), "no signature matches the body"},
		"timestamp not signed": {"t=1790000001,v1=" + good, "no signature matches the body"},
	} {
		if got := CheckWebhookSignature(secret, body, test.signature, now, 5*time.Minute); got != test.want {
			t.Fatalf("%s: want %q, got %q", name, test.want, got)
		}
	}
}
