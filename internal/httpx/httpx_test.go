package httpx

import (
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
