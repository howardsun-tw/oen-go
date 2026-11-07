package oentest

import (
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFailHTTPWritesStatusWithEmptyBody(t *testing.T) {
	server := New()
	t.Cleanup(server.Close)
	server.FailHTTP("/token/transactions", http.StatusBadGateway)

	request, err := http.NewRequest(http.MethodPost, server.URL+"/token/transactions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+DefaultAuthToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d want %d", response.StatusCode, http.StatusBadGateway)
	}
	if len(body) != 0 {
		t.Fatalf("body=%q; FailHTTP must not invent an envelope", body)
	}
}

// A client that gives up on a hung request must not leave the fake holding a
// handler and a connection until Close: a long-lived fake would pile them up.
func TestHangReleasesTheHandlerWhenTheClientGoesAway(t *testing.T) {
	server := New()
	t.Cleanup(server.Close)
	server.Hang("/token/transactions")
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Timeout: 50 * time.Millisecond, Transport: transport}

	before := runtime.NumGoroutine()
	for range 5 {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/token/transactions", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+DefaultAuthToken)
		if _, err := client.Do(request); err == nil {
			t.Fatal("expected hang to fail the client request")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutines=%d after the clients left, want at most %d", got, before)
	}
}

func TestHangDropsConnectionInsteadOfEmptyOK(t *testing.T) {
	server := New()
	t.Cleanup(server.Close)
	server.Hang("/token/transactions")

	request, err := http.NewRequest(http.MethodPost, server.URL+"/token/transactions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+DefaultAuthToken)

	client := &http.Client{Timeout: 80 * time.Millisecond}
	started := time.Now()
	_, err = client.Do(request)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("expected hang to fail the client request")
	}
	if elapsed < 60*time.Millisecond {
		t.Fatalf("hang returned too quickly (%s); empty 200 would look like this", elapsed)
	}
}
