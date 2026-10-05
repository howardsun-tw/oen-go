package oen

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/howardsun-tw/oen-go/oentest"
)

// documentedStoreRow uses every field in the response table of
// 查詢商店定期購訂單列表. Oen publishes no response example for this
// endpoint, so the values are constructed from the table.
const documentedStoreRow = `{
	"id": "sub-store-1",
	"status": "retryScheduled",
	"items": [{"name": "咖啡豆", "quantity": 1}],
	"totalAmount": 599,
	"period": 3,
	"numberOfPeriods": 12,
	"lastChargedAt": "2026-09-01T09:00:00+08:00",
	"user": {"name": "王小明", "email": "ming@example.com"},
	"createdAt": "2026-07-01T01:00:00Z",
	"cancelledAt": null,
	"endedAt": "2027-06-01T09:00:00+08:00"
}`

func TestListStoreSubscriptionsReadsEveryDocumentedField(t *testing.T) {
	server, client := newFake(t)
	server.Success("/subscriptions", json.RawMessage(`{"subscriptions":[`+documentedStoreRow+`],"page":"next-token"}`))

	page, err := client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{})
	noError(t, err)
	equal(t, "next-token", page.NextPage)
	equal(t, 1, len(page.Subscriptions))
	got := page.Subscriptions[0]
	equal(t, "sub-store-1", got.ID)
	equal(t, StoreSubscriptionRetryScheduled, got.Status)
	isTrue(t, got.Status.Known())
	equal(t, Amount(599), got.TotalAmount)
	isTrue(t, got.HasTotalAmount)
	equal(t, 3, got.Period)
	equal(t, 12, got.NumberOfPeriods)
	jsonEqual(t, `[{"name":"咖啡豆","quantity":1}]`, string(got.Items))
	equal(t, Customer{Name: "王小明", Email: "ming@example.com"}, got.Customer)
	equal(t, time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC), got.LastChargedAt.UTC())
	equal(t, time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC), got.CreatedAt.UTC())
	isTrue(t, got.CancelledAt == nil)
	equal(t, time.Date(2027, 6, 1, 1, 0, 0, 0, time.UTC), got.EndedAt.UTC())
	contains(t, string(got.RedactedPayload), `"sub-store-1"`)

	request := server.LastRequest("/subscriptions")
	equal(t, http.MethodGet, request.Method)
	equal(t, "/subscriptions", request.Path)
	equal(t, "", request.Query)
}

func TestListStoreSubscriptionsSendsStatusAndPage(t *testing.T) {
	server, client := newFake(t)
	_, err := client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{
		Statuses: []StoreSubscriptionStatus{StoreSubscriptionOngoing, StoreSubscriptionRetryScheduled},
		Page:     "tok+/=",
	})
	noError(t, err)
	equal(t, "page=tok%2B%2F%3D&status=ongoing%2CretryScheduled", server.LastRequest("/subscriptions").Query)
}

func TestListStoreSubscriptionsRefusesUnknownStatus(t *testing.T) {
	server, client := newFake(t)
	for _, status := range []StoreSubscriptionStatus{"waiting", "ongoing,done", ""} {
		_, err := client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{
			Statuses: []StoreSubscriptionStatus{status},
		})
		errIs(t, err, ErrInvalidInput)
	}
	equal(t, 0, server.Count("/subscriptions"))
}

func TestListStoreSubscriptionsDefaultFakeIsEmpty(t *testing.T) {
	server, client := newFake(t)
	server.SetNextPage("p2")
	page, err := client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{})
	noError(t, err)
	equal(t, 0, len(page.Subscriptions))
	equal(t, "p2", page.NextPage)
}

func TestListStoreSubscriptionsOmittedFieldsStayEmpty(t *testing.T) {
	server, client := newFake(t)
	server.Success("/subscriptions", json.RawMessage(`{"subscriptions":[{}],"page":null}`))
	page, err := client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{})
	noError(t, err)
	equal(t, "", page.NextPage)
	got := page.Subscriptions[0]
	isFalse(t, got.HasTotalAmount)
	isFalse(t, got.Status.Known())
	isTrue(t, got.Items == nil)
	isTrue(t, got.LastChargedAt == nil)
}

func TestListStoreSubscriptionsRejectsMalformedResponses(t *testing.T) {
	for _, data := range []string{
		`{"page":null}`,
		`{"subscriptions":null,"page":null}`,
		`{"subscriptions":{},"page":null}`,
		`[]`,
		`{"subscriptions":[],"page":7}`,
		`{"subscriptions":[{"totalAmount":"12.5"}],"page":null}`,
		`{"subscriptions":[{"period":"three"}],"page":null}`,
		`{"subscriptions":[{"items":{"name":"x"}}],"page":null}`,
		`{"subscriptions":[{"user":"王小明"}],"page":null}`,
		`{"subscriptions":[{"endedAt":"someday"}],"page":null}`,
	} {
		server, client := newFake(t)
		server.Success("/subscriptions", json.RawMessage(data))
		_, err := client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{})
		errIs(t, err, ErrUnknownOutcome)
	}
}

func TestStoreSubscriptionsAreSanitized(t *testing.T) {
	server, client := newFake(t)
	server.Success("/subscriptions", json.RawMessage(
		`{"subscriptions":[{"id":"s1","paymentInfo":{"cardNumber":"4242424242424242","token":"tok_x"}}],"page":null}`))
	page, err := client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{})
	noError(t, err)
	notContains(t, string(page.Subscriptions[0].RedactedPayload), "4242424242424242")
	notContains(t, string(page.Subscriptions[0].RedactedPayload), "tok_x")
}

func TestListStoreSubscriptionsReportsUnauthorized(t *testing.T) {
	server := oentest.New()
	t.Cleanup(server.Close)
	cfg := contractConfig(server.URL)
	cfg.AuthToken = "wrong"
	client, err := New(cfg)
	noError(t, err)
	_, err = client.ListStoreSubscriptions(context.Background(), ListStoreSubscriptionsRequest{})
	errIs(t, err, ErrUnauthorized)
}
