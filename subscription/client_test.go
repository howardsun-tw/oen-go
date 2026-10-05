package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "sub_sk_0123456789abcdef0123456789abcdef"

type recorded struct {
	Method  string
	Path    string
	RawPath string
	Query   string
	Header  http.Header
	Body    string
}

type fake struct {
	mu       sync.Mutex
	requests []recorded
	respond  func(w http.ResponseWriter, r *http.Request)
}

func (f *fake) last(t *testing.T) recorded {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("no request reached the fake")
	}
	return f.requests[len(f.requests)-1]
}

func (f *fake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// newFake answers every request with status and body unless respond is
// replaced.
func newFake(t *testing.T, status int, body string) (*fake, *Client) {
	t.Helper()
	f := &fake{respond: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{
			Method: r.Method, Path: r.URL.Path, RawPath: r.URL.EscapedPath(),
			Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(raw),
		})
		respond := f.respond
		f.mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{APIKey: testKey, BaseURL: server.URL})
	noError(t, err)
	return f, client
}

type endpointCase struct {
	name   string
	method string
	path   string
	keyed  bool
	// body is the JSON the SDK must send, empty for no body.
	body string
	call func(context.Context, *Client) error
}

const testIdempotencyKey = "idem-0001"

// endpointCases lists all 23 endpoints in Oen's Subscription API table.
func endpointCases() []endpointCase {
	keyed := func(fields map[string]any) KeyedRequest {
		return KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: testIdempotencyKey, Fields: fields}
	}
	discard := func(_ any, err error) error { return err }
	return []endpointCase{
		{"CreateProduct", "POST", "/v1/products", false,
			`{"name":"Pro","subscriptionType":"fixedPeriod"}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.CreateProduct(ctx, CreateProductRequest{Name: "Pro", SubscriptionType: SubscriptionTypeFixedPeriod}))
			}},
		{"ListProducts", "GET", "/v1/products", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.ListProducts(ctx, "")) }},
		{"GetProduct", "GET", "/v1/products/prod_1", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.GetProduct(ctx, "prod_1")) }},
		{"UpdateProduct", "PUT", "/v1/products/prod_1", false, `{"name":"Pro+"}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.UpdateProduct(ctx, UpdateProductRequest{ProductID: "prod_1", Fields: map[string]any{"name": "Pro+"}}))
			}},
		{"GetSubscriptionURL", "GET", "/v1/products/prod_1/subscription-url", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.GetSubscriptionURL(ctx, "prod_1")) }},
		{"CreatePlan", "POST", "/v1/products/prod_1/plans", false,
			`{"name":"Monthly","price":299,"billingPeriod":{"unit":"month","interval":1}}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.CreatePlan(ctx, CreatePlanRequest{
					ProductID: "prod_1", Name: "Monthly", Price: 299,
					BillingPeriod: BillingPeriod{Unit: PeriodMonth, Interval: 1},
				}))
			}},
		{"ListPlans", "GET", "/v1/products/prod_1/plans", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.ListPlans(ctx, "prod_1", "")) }},
		{"UpdatePlan", "PUT", "/v1/products/prod_1/plans/plan_1", false, `{"price":399}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.UpdatePlan(ctx, UpdatePlanRequest{ProductID: "prod_1", PlanID: "plan_1", Fields: map[string]any{"price": 399}}))
			}},
		{"ListProductSubscriptions", "GET", "/v1/products/prod_1/subscriptions", false, "",
			func(ctx context.Context, c *Client) error {
				return discard(c.ListProductSubscriptions(ctx, "prod_1", ""))
			}},
		{"GetSubscription", "GET", "/v1/subscriptions/sub_1", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.GetSubscription(ctx, "sub_1")) }},
		{"ChangePlan", "POST", "/v1/subscriptions/sub_1/plan-change", true, `{"planId":"plan_2"}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.ChangePlan(ctx, keyed(map[string]any{"planId": "plan_2"})))
			}},
		{"ChangePeriod", "POST", "/v1/subscriptions/sub_1/period-change", true, `{"end":"2026-12-31"}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.ChangePeriod(ctx, keyed(map[string]any{"end": "2026-12-31"})))
			}},
		{"CancelSubscription", "POST", "/v1/subscriptions/sub_1/cancel", true, `{}`,
			func(ctx context.Context, c *Client) error { return discard(c.CancelSubscription(ctx, keyed(nil))) }},
		{"ResumeSubscription", "POST", "/v1/subscriptions/sub_1/resume", true, `{}`,
			func(ctx context.Context, c *Client) error { return discard(c.ResumeSubscription(ctx, keyed(nil))) }},
		{"TerminateSubscription", "POST", "/v1/subscriptions/sub_1/terminate", true, `{}`,
			func(ctx context.Context, c *Client) error { return discard(c.TerminateSubscription(ctx, keyed(nil))) }},
		{"RefundSubscription", "POST", "/v1/subscriptions/sub_1/refunds", true, `{"amount":100}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.RefundSubscription(ctx, keyed(map[string]any{"amount": 100})))
			}},
		{"CreateRecoveryLink", "POST", "/v1/subscriptions/sub_1/recovery-links", false, `{}`,
			func(ctx context.Context, c *Client) error { return discard(c.CreateRecoveryLink(ctx, "sub_1")) }},
		{"ListCustomers", "GET", "/v1/customers", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.ListCustomers(ctx, "")) }},
		{"GetCustomer", "GET", "/v1/customers/cus_1", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.GetCustomer(ctx, "cus_1")) }},
		{"UpdateCustomer", "PUT", "/v1/customers/cus_1", false, `{"name":"王小明"}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.UpdateCustomer(ctx, UpdateCustomerRequest{CustomerID: "cus_1", Fields: map[string]any{"name": "王小明"}}))
			}},
		{"ListWebhookDeliveries", "GET", "/v1/webhook-deliveries", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.ListWebhookDeliveries(ctx, "")) }},
		{"GetWebhookDelivery", "GET", "/v1/webhook-deliveries/del_1", false, "",
			func(ctx context.Context, c *Client) error { return discard(c.GetWebhookDelivery(ctx, "del_1")) }},
		{"ResendWebhookDelivery", "POST", "/v1/webhook-deliveries/del_1/resend", true, `{}`,
			func(ctx context.Context, c *Client) error {
				return discard(c.ResendWebhookDelivery(ctx, "del_1", testIdempotencyKey))
			}},
	}
}

func successFor(method, path string) string {
	switch {
	case method == http.MethodGet && (strings.HasSuffix(path, "s") && !strings.HasSuffix(path, "/subscription-url")):
		return `{"data":[{"id":"x_1","status":"active"}],"paging":{"next":null}}`
	default:
		return `{"data":{"id":"x_1","status":"active"}}`
	}
}

func TestEveryEndpointSendsItsDocumentedRequest(t *testing.T) {
	cases := endpointCases()
	equal(t, 23, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, client := newFake(t, http.StatusOK, successFor(tc.method, tc.path))
			noError(t, tc.call(context.Background(), client))

			got := f.last(t)
			equal(t, tc.method, got.Method)
			equal(t, tc.path, got.Path)
			equal(t, "Bearer "+testKey, got.Header.Get("Authorization"))
			if tc.keyed {
				equal(t, testIdempotencyKey, got.Header.Get("Idempotency-Key"))
			} else {
				equal(t, "", got.Header.Get("Idempotency-Key"))
			}
			if tc.body == "" {
				equal(t, "", got.Body)
				equal(t, "", got.Header.Get("Content-Type"))
			} else {
				jsonEqual(t, tc.body, got.Body)
				equal(t, "application/json", got.Header.Get("Content-Type"))
			}
		})
	}
}

func TestKeyedEndpointsRefuseAMissingKeyWithoutSending(t *testing.T) {
	for _, tc := range endpointCases() {
		if !tc.keyed {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			f, client := newFake(t, http.StatusOK, `{"data":{}}`)
			err := callWithoutKey(t, tc.name, client)
			errIs(t, err, ErrInvalidInput)
			equal(t, "idempotencyKey", fieldOf(t, err))
			equal(t, 0, f.count())
		})
	}
}

func callWithoutKey(t *testing.T, name string, c *Client) error {
	t.Helper()
	ctx := context.Background()
	req := KeyedRequest{SubscriptionID: "sub_1", Fields: map[string]any{"x": 1}}
	var err error
	switch name {
	case "ChangePlan":
		_, err = c.ChangePlan(ctx, req)
	case "ChangePeriod":
		_, err = c.ChangePeriod(ctx, req)
	case "CancelSubscription":
		_, err = c.CancelSubscription(ctx, req)
	case "ResumeSubscription":
		_, err = c.ResumeSubscription(ctx, req)
	case "TerminateSubscription":
		_, err = c.TerminateSubscription(ctx, req)
	case "RefundSubscription":
		_, err = c.RefundSubscription(ctx, req)
	case "ResendWebhookDelivery":
		_, err = c.ResendWebhookDelivery(ctx, "del_1", "")
	default:
		t.Fatalf("no keyed call for %s", name)
	}
	return err
}

func TestEveryEndpointReportsRateLimitsWithoutRetrying(t *testing.T) {
	for _, tc := range endpointCases() {
		t.Run(tc.name, func(t *testing.T) {
			f, client := newFake(t, http.StatusTooManyRequests, `{"errno":"X999","message":"slow down"}`)
			f.respond = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(http.StatusTooManyRequests)
			}
			err := tc.call(context.Background(), client)
			errIs(t, err, ErrRateLimited)
			equal(t, tc.method != http.MethodGet, IsUnknownOutcome(err))
			equal(t, 7*time.Second, RetryAfter(err))
			equal(t, tc.keyed, RetryableWithSameKey(err))
			equal(t, 1, f.count())
		})
	}
}

func TestEveryEndpointReportsServerErrorsAsUnknownOutcome(t *testing.T) {
	for _, tc := range endpointCases() {
		t.Run(tc.name, func(t *testing.T) {
			f, client := newFake(t, http.StatusServiceUnavailable, `<html>down</html>`)
			err := tc.call(context.Background(), client)
			errIs(t, err, ErrUnknownOutcome)
			equal(t, tc.keyed, RetryableWithSameKey(err))
			equal(t, 1, f.count())
		})
	}
}

func TestEveryEndpointTreatsAnUnreadableSuccessAsUnknownOutcome(t *testing.T) {
	for _, tc := range endpointCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newFake(t, http.StatusOK, `not json`)
			err := tc.call(context.Background(), client)
			errIs(t, err, ErrUnknownOutcome)
			var detail *Error
			isTrue(t, errors.As(err, &detail))
			equal(t, http.StatusOK, detail.HTTPStatus)
		})
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	t.Cleanup(target.Close)
	f, client := newFake(t, 0, "")
	f.respond = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}
	_, err := client.CancelSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
	errIs(t, err, ErrUnknownOutcome)
	isFalse(t, followed.Load())
	equal(t, 1, f.count())
}

func TestTransportFailureIsUnknownOutcome(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	client, err := New(Config{APIKey: testKey, BaseURL: server.URL})
	noError(t, err)
	_, err = client.RefundSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
	errIs(t, err, ErrUnknownOutcome)
	isTrue(t, RetryableWithSameKey(err))
	_, err = client.CreateRecoveryLink(context.Background(), "sub_1")
	errIs(t, err, ErrUnknownOutcome)
	isFalse(t, RetryableWithSameKey(err))
}

func TestTimeoutIsUnknownOutcome(t *testing.T) {
	release := make(chan struct{})
	f, client := newFake(t, 0, "")
	f.respond = func(http.ResponseWriter, *http.Request) { <-release }
	defer close(release)
	client.cfg.Timeout = 20 * time.Millisecond
	_, err := client.TerminateSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
	errIs(t, err, ErrUnknownOutcome)
	errIs(t, err, context.DeadlineExceeded)
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	f, client := newFake(t, http.StatusOK, `{"data":{"id":"a/b"}}`)
	_, err := client.GetSubscription(context.Background(), "a/b?c")
	noError(t, err)
	equal(t, "/v1/subscriptions/a%2Fb%3Fc", f.last(t).RawPath)
	equal(t, "", f.last(t).Query)

	for _, id := range []string{"", "  ", ".", ".."} {
		_, err := client.GetSubscription(context.Background(), id)
		errIs(t, err, ErrInvalidInput)
	}
	equal(t, 1, f.count())
}

func TestPageTokenIsSentUnchanged(t *testing.T) {
	f, client := newFake(t, http.StatusOK, `{"data":[],"paging":{"next":"tok+/=="}}`)
	page, err := client.ListCustomers(context.Background(), "prev+/==")
	noError(t, err)
	equal(t, "page=prev%2B%2F%3D%3D", f.last(t).Query)
	equal(t, "tok+/==", page.Next)
	equal(t, 0, len(page.Items))
}

func TestLogNeverContainsTheKey(t *testing.T) {
	var lines []string
	_, client := newFake(t, http.StatusOK, `{"data":{"id":"sub_1"}}`)
	client.cfg.Logf = func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	_, err := client.GetSubscription(context.Background(), "sub_1")
	noError(t, err)
	equal(t, 1, len(lines))
	contains(t, lines[0], "path=/v1/subscriptions/sub_1")
	contains(t, lines[0], "outcome=success")
	notContains(t, lines[0], testKey)
}

func TestNilContextIsRefused(t *testing.T) {
	f, client := newFake(t, http.StatusOK, `{"data":{}}`)
	var ctx context.Context // nil: exercises the guard
	_, err := client.GetCustomer(ctx, "cus_1")
	errIs(t, err, ErrInvalidInput)
	equal(t, 0, f.count())
}

// A keyed POST carries Idempotency-Key, which net/http treats as permission
// to replay the request on a fresh connection when a reused one fails. The
// SDK promises one request per call, so that replay must not happen.
func TestKeyedWriteIsNotReplayedByTheTransport(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"data":{"id":"sub_1"}}`)
			return
		}
		posts.Add(1)
		// Read the whole request, then drop the connection without a reply,
		// as a server that applied the refund and then crashed would.
		_, _ = io.Copy(io.Discard, r.Body)
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = buffered.Flush()
		_ = conn.Close()
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{APIKey: testKey, BaseURL: server.URL})
	noError(t, err)

	// Leave an idle keep-alive connection for the POST to reuse.
	_, err = client.GetSubscription(context.Background(), "sub_1")
	noError(t, err)
	_, err = client.RefundSubscription(context.Background(), KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k-1"})
	errIs(t, err, ErrUnknownOutcome)
	equal(t, int32(1), posts.Load())
}

func TestSuccessWithoutDataIsUnknownOutcome(t *testing.T) {
	for _, tc := range endpointCases() {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newFake(t, http.StatusOK, `{}`)
			err := tc.call(context.Background(), client)
			errIs(t, err, ErrUnknownOutcome)
		})
	}
}
