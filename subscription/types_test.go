package subscription

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	when, err := time.Parse(time.RFC3339, value)
	noError(t, err)
	return when
}

func TestGetSubscriptionReadsDocumentedFieldsAndKeepsTheRest(t *testing.T) {
	_, client := newFake(t, http.StatusOK, `{"data":{
		"id":"sub_1","status":"paused","nextChargeAt":"2026-10-06T01:00:00Z",
		"customer":{"id":"cus_1"},
		"payment":{"token":"tok_secret","cardNumber":"4111111111111111"}
	}}`)
	got, err := client.GetSubscription(context.Background(), "sub_1")
	noError(t, err)
	equal(t, "sub_1", got.ID)
	equal(t, StatusPaused, got.Status)
	isTrue(t, got.Status.Known())
	equal(t, mustTime(t, "2026-10-06T09:00:00+08:00").Unix(), got.NextChargeAt.Unix())
	contains(t, string(got.Raw), `"customer":{"id":"cus_1"}`)
	notContains(t, string(got.Raw), "tok_secret")
	notContains(t, string(got.Raw), "4111111111111111")
	contains(t, string(got.Raw), `"cardNumber":"1111"`)
}

func TestNextChargeAtFormats(t *testing.T) {
	tests := []struct {
		raw  string
		want int64
		ok   bool
	}{
		{`"2026-10-06T09:00:00+08:00"`, 1791248400, true},
		{`1791248400`, 1791248400, true},
		{`null`, 0, true},
		{`""`, 0, true},
		{`"tomorrow"`, 0, false},
		{`"1791248400"`, 0, false},
		{`1791248400.5`, 0, false},
		{`true`, 0, false},
	}
	for _, tc := range tests {
		got, err := decodeSubscription([]byte(`{"id":"sub_1","nextChargeAt":` + tc.raw + `}`))
		if !tc.ok {
			if err == nil {
				t.Fatalf("%s: want an error", tc.raw)
			}
			continue
		}
		noError(t, err)
		if tc.want == 0 {
			isTrue(t, got.NextChargeAt == nil, tc.raw)
			continue
		}
		equal(t, tc.want, got.NextChargeAt.Unix(), tc.raw)
	}
}

func TestUnknownStatusPassesThrough(t *testing.T) {
	got, err := decodeSubscription([]byte(`{"id":"sub_1","status":"archived"}`))
	noError(t, err)
	equal(t, Status("archived"), got.Status)
	isFalse(t, got.Status.Known())
}

func TestMalformedTypedFieldsAreErrors(t *testing.T) {
	for _, raw := range []string{`{"id":1}`, `{"status":true}`, `[]`, `"sub_1"`} {
		if _, err := decodeSubscription([]byte(raw)); err == nil {
			t.Fatalf("%s: want an error", raw)
		}
	}
	_, client := newFake(t, http.StatusOK, `{"data":{"id":123}}`)
	_, err := client.GetCustomer(context.Background(), "cus_1")
	errIs(t, err, ErrUnknownOutcome)
}

func TestPagesReadDataAndPaging(t *testing.T) {
	tests := []struct {
		body  string
		items int
		next  string
		ok    bool
	}{
		{`{"data":[{"id":"sub_1"},{"id":"sub_2"}],"paging":{"next":"p2"}}`, 2, "p2", true},
		{`{"data":[{"id":"sub_1"}],"paging":{"next":null}}`, 1, "", true},
		{`{"data":[],"paging":null}`, 0, "", true},
		{`{"data":[]}`, 0, "", true},
		{`{"data":[],"paging":{"next":42}}`, 0, "", false},
		{`{"data":[],"paging":"p2"}`, 0, "", false},
		{`{"data":{"id":"sub_1"}}`, 0, "", false},
		{`{"data":null}`, 0, "", false},
		{`{"data":[{"id":"sub_1","status":1}]}`, 0, "", false},
	}
	for _, tc := range tests {
		_, client := newFake(t, http.StatusOK, tc.body)
		page, err := client.ListProductSubscriptions(context.Background(), "prod_1", "")
		if !tc.ok {
			errIs(t, err, ErrUnknownOutcome)
			continue
		}
		noError(t, err)
		equal(t, tc.items, len(page.Items), tc.body)
		equal(t, tc.next, page.Next, tc.body)
	}
}

func TestResultKeepsUndocumentedDataSanitized(t *testing.T) {
	_, client := newFake(t, http.StatusOK, `{"data":{"refundEstimate":{"amount":150},"cvv":"123"}}`)
	got, err := client.TerminateSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
	noError(t, err)
	jsonEqual(t, `{"refundEstimate":{"amount":150},"cvv":"REDACTED"}`, string(got.Data))

	_, client = newFake(t, http.StatusOK, `{"data":null}`)
	got, err = client.ResumeSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
	noError(t, err)
	equal(t, 0, len(got.Data))

	_, client = newFake(t, http.StatusOK, `{"data":"https://sub.oen.tw/p/prod_1"}`)
	got, err = client.GetSubscriptionURL(context.Background(), "prod_1")
	noError(t, err)
	equal(t, `"https://sub.oen.tw/p/prod_1"`, string(got.Data))
}
