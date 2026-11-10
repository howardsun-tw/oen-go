package oentest

import (
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"slices"
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

// send performs one authorized request and returns the status and body.
func send(t *testing.T, client *http.Client, server *Server, method, path, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+DefaultAuthToken)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

// A hung charge must not be recorded behind the caller's back: only
// RecordCharge may apply a request the caller cannot see.
func TestHangAppliesNothing(t *testing.T) {
	server := New()
	t.Cleanup(server.Close)
	server.Hang("/token/transactions")

	request, err := http.NewRequest(http.MethodPost, server.URL+"/token/transactions", strings.NewReader(`{"orderId":"O1","amount":1000}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+DefaultAuthToken)
	if _, err := (&http.Client{Timeout: 50 * time.Millisecond}).Do(request); err == nil {
		t.Fatal("expected hang to fail the client request")
	}

	_, body := send(t, http.DefaultClient, server, http.MethodGet, "/order/O1/transactions", "")
	if !strings.Contains(body, `"transactions":[]`) {
		t.Fatalf("body=%s; a hung charge must leave no transaction", body)
	}
	if got := server.Count("/token/transactions"); got != 1 {
		t.Fatalf("recorded requests=%d; a hung request is still recorded", got)
	}
}

// S0000 is the default only for a 2xx status. On a failure status it would
// reach the SDK as an error that carries the success code.
func TestFailureStatusWithoutCodeCarriesNoSuccessCode(t *testing.T) {
	server := New()
	t.Cleanup(server.Close)
	server.ChargeThenFail("/token/transactions", http.StatusGatewayTimeout)
	server.QueueResponse("/token/transactions", Response{})

	status, body := send(t, http.DefaultClient, server, http.MethodPost, "/token/transactions", `{"orderId":"O1","amount":1000}`)
	if status != http.StatusGatewayTimeout || strings.Contains(body, successCode) {
		t.Fatalf("status=%d body=%s; want 504 without %s", status, body, successCode)
	}
	status, body = send(t, http.DefaultClient, server, http.MethodPost, "/token/transactions", `{}`)
	if status != http.StatusOK || !strings.Contains(body, `"code":"S0000"`) {
		t.Fatalf("status=%d body=%s; a 2xx without a code is still S0000", status, body)
	}
	// Oen answers an unknown path in plain text, not with an envelope.
	status, body = send(t, http.DefaultClient, server, http.MethodGet, "/no-such-endpoint", "")
	if status != http.StatusNotFound || body != "Not Found" {
		t.Fatalf("status=%d body=%q; want 404 with the plain-text body Not Found", status, body)
	}
}

// Count and LastRequest take the method-qualified form QueueResponse takes.
func TestCountAndLastRequestAcceptAMethod(t *testing.T) {
	server := New()
	t.Cleanup(server.Close)
	send(t, http.DefaultClient, server, http.MethodPut, "/subscriptions/S1", `{}`)
	send(t, http.DefaultClient, server, http.MethodGet, "/subscriptions/S1", "")

	if got := server.Count("PUT /subscriptions/{subscriptionId}"); got != 1 {
		t.Fatalf("PUT count=%d want 1", got)
	}
	if got := server.Count("/subscriptions/{subscriptionId}"); got != 2 {
		t.Fatalf("route count=%d want 2", got)
	}
	if got := server.LastRequest("put /subscriptions/S1").Method; got != http.MethodPut {
		t.Fatalf("last PUT method=%q", got)
	}
	if got := server.LastRequest("/subscriptions/S1").Method; got != http.MethodGet {
		t.Fatalf("last method=%q want GET", got)
	}
	// A path with a space in it is a path, with or without its leading slash.
	send(t, http.DefaultClient, server, http.MethodGet, "/order/A%20B/transactions", "")
	for _, endpoint := range []string{"/order/A B/transactions", "order/A B/transactions"} {
		if got := server.Count(endpoint); got != 1 {
			t.Fatalf("Count(%q)=%d want 1", endpoint, got)
		}
	}
}

// Oen lists transactions newest first; map order would make that random.
func TestTransactionListIsNewestFirst(t *testing.T) {
	server := New()
	t.Cleanup(server.Close)
	// The expected order is the reverse of the IDs the charges returned.
	want := make([]string, 6)
	for i := range want {
		_, body := send(t, http.DefaultClient, server, http.MethodPost, "/token/transactions", `{"orderId":"O1","amount":1000}`)
		var charged struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &charged); err != nil || charged.Data.ID == "" {
			t.Fatalf("charge %d: body=%s err=%v", i, body, err)
		}
		// Equal IDs would make any order look right.
		if slices.Contains(want, charged.Data.ID) {
			t.Fatalf("charge %d repeated the ID %q", i, charged.Data.ID)
		}
		want[len(want)-1-i] = charged.Data.ID
	}

	_, body := send(t, http.DefaultClient, server, http.MethodGet, "/transactions", "")
	var envelope struct {
		Data struct {
			Transactions []struct {
				ID string `json:"id"`
			} `json:"transactions"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(envelope.Data.Transactions))
	for i, transaction := range envelope.Data.Transactions {
		got[i] = transaction.ID
	}
	if !slices.Equal(got, want) {
		t.Fatalf("order=%v want %v", got, want)
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
