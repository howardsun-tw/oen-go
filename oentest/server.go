// Package oentest provides a programmable fake of the Oen Tech Payment API.
//
// It is a real httptest.Server: tests point an oen.Client at its URL and get
// documented default responses, or queue their own to exercise refusals,
// rate limits, malformed bodies and outcomes the caller cannot tell apart.
//
// The package deliberately does not import the SDK, so the SDK's own tests can
// use it without an import cycle.
package oentest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults mirror the identifiers in Oen's published examples.
const (
	DefaultMerchantID     = "oentech"
	DefaultAuthToken      = "auth-token"
	DefaultCheckoutID     = "2HhndgEquCbDzC5OyVxWSGZmd2l"
	DefaultTransactionHID = "P20240412QJMAZNML"
	DefaultTransactionID  = "2ezVvlPIKZzJxY8L2ukhEPKDJWz"
	DefaultSubscriptionID = "S20240412YOARH7GM"
	DefaultAuthCode       = "831000"

	successCode = "S0000"
)

// Response is one queued fake response. A non-nil Body is written verbatim;
// otherwise the fake writes an Oen envelope built from Code, Message and Data.
type Response struct {
	StatusCode int
	Code       string
	Message    string
	Data       any
	Body       []byte
	Headers    http.Header
	Delay      time.Duration
	Block      bool

	// RecordCharge makes a charge endpoint record the transaction before
	// writing this response. It is the only way to express the outcome a
	// caller cannot tell apart from a charge that never happened: Oen took the
	// money and the caller saw a timeout or a 5xx.
	RecordCharge bool
}

// Request is one request the fake received, with a copy of its raw body.
type Request struct {
	Method string
	// Path is the path after the server decoded it, which is what a handler
	// routes on.
	Path string
	// RawPath is the path as it arrived on the wire, with percent-encoding
	// intact. Use it to assert that identifiers were escaped.
	RawPath string
	Query   string
	Header  http.Header
	Body    []byte
}

// Server is a programmable Oen API fake.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	merchantID     string
	authToken      string
	checkoutID     string
	transactionHID string
	transactionID  string
	subscriptionID string
	authCode       string
	cardNum        string
	cardType       string
	nextPageToken  string
	rejectBadToken bool

	chargeCount         int
	requests            []Request
	responses           map[string][]Response
	blocked             map[string]bool
	transactionsByID    map[string]map[string]any
	transactionsByOrder map[string][]map[string]any
	subscriptions       map[string]map[string]any
}

// New starts a fake that answers every documented endpoint successfully.
func New() *Server {
	server := &Server{
		merchantID:          DefaultMerchantID,
		authToken:           DefaultAuthToken,
		checkoutID:          DefaultCheckoutID,
		transactionHID:      DefaultTransactionHID,
		transactionID:       DefaultTransactionID,
		subscriptionID:      DefaultSubscriptionID,
		authCode:            DefaultAuthCode,
		cardNum:             "4242424242424242",
		cardType:            "Visa",
		rejectBadToken:      true,
		responses:           make(map[string][]Response),
		blocked:             make(map[string]bool),
		transactionsByID:    make(map[string]map[string]any),
		transactionsByOrder: make(map[string][]map[string]any),
		subscriptions:       make(map[string]map[string]any),
	}
	server.Server = httptest.NewServer(http.HandlerFunc(server.serve))
	return server
}

// QueueResponse queues responses for one endpoint in FIFO order. The endpoint
// may be a concrete path ("/token/transactions"), a route template
// ("/transactions/{id}") or a method-qualified route ("PUT /subscriptions/{subscriptionId}").
func (s *Server) QueueResponse(endpoint string, responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := normalizeKey(endpoint)
	s.responses[key] = append(s.responses[key], responses...)
}

// Success queues one successful envelope.
func (s *Server) Success(endpoint string, data any) {
	s.QueueResponse(endpoint, Response{Code: successCode, Data: data})
}

// Reject queues a business failure envelope, such as T0004 for a card with no
// funds left.
func (s *Server) Reject(endpoint, code, message string) {
	s.QueueResponse(endpoint, Response{Code: code, Message: message})
}

// FailHTTP queues an HTTP failure with no body.
func (s *Server) FailHTTP(endpoint string, status int) {
	s.QueueResponse(endpoint, Response{StatusCode: status})
}

// Malformed queues a 2xx whose body is not JSON.
func (s *Server) Malformed(endpoint, body string) {
	s.QueueResponse(endpoint, Response{StatusCode: http.StatusOK, Body: []byte(body)})
}

// ChargeThenFail queues a charge Oen accepts and then fails to report: the
// transaction is recorded and findable by order, but the caller sees status.
func (s *Server) ChargeThenFail(endpoint string, status int) {
	s.QueueResponse(endpoint, Response{StatusCode: status, RecordCharge: true})
}

// Hang blocks an endpoint until the request context is cancelled. The bounded
// fallback keeps Close from hanging on a faulty test.
func (s *Server) Hang(endpoint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked[normalizeKey(endpoint)] = true
}

// Unhang removes a block.
func (s *Server) Unhang(endpoint string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blocked, normalizeKey(endpoint))
}

// SetPaymentInfo changes the card data in default transaction resources.
func (s *Server) SetPaymentInfo(cardNum, cardType string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardNum = cardNum
	s.cardType = cardType
}

// SetNextPage makes list endpoints return a page token.
func (s *Server) SetNextPage(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextPageToken = token
}

// AcceptAnyToken stops the fake from checking the Authorization header, for
// tests that care about something else.
func (s *Server) AcceptAnyToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectBadToken = false
}

// authorized reports whether the request carried this merchant's bearer token.
// Oen answers an unauthorized request with A0001, and a fake that accepted
// anything could not catch a client that stopped sending the header.
func (s *Server) authorized(request *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.rejectBadToken {
		return true
	}
	return request.Header.Get("Authorization") == "Bearer "+s.authToken
}

// Requests returns copies of every request the fake received.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]Request, len(s.requests))
	for i, request := range s.requests {
		requests[i] = cloneRequest(request)
	}
	return requests
}

// Count returns how many requests an endpoint received.
func (s *Server) Count(endpoint string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := routeKey(normalizePath(endpoint))
	count := 0
	for _, request := range s.requests {
		if routeKey(request.Path) == key {
			count++
		}
	}
	return count
}

// LastRequest returns the most recent request for an endpoint.
func (s *Server) LastRequest(endpoint string) Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := routeKey(normalizePath(endpoint))
	for i := len(s.requests) - 1; i >= 0; i-- {
		if routeKey(s.requests[i].Path) == key {
			return cloneRequest(s.requests[i])
		}
	}
	return Request{}
}

func (s *Server) serve(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	path := normalizePath(request.URL.Path)
	s.recordRequest(Request{
		Method:  request.Method,
		Path:    path,
		RawPath: request.URL.EscapedPath(),
		Query:   request.URL.RawQuery,
		Header:  request.Header.Clone(),
		Body:    append([]byte(nil), body...),
	})

	if !s.authorized(request) {
		s.writeResponse(writer, Response{
			StatusCode: http.StatusUnauthorized,
			Code:       "A0001",
			Message:    "unauthorized",
		})
		return
	}

	response, queued := s.nextResponse(request.Method, path)
	if !queued {
		response = s.defaultResponse(request.Method, path, body)
	} else if response.RecordCharge {
		// Record the charge, then throw its envelope away: the caller gets the
		// queued failure while the transaction stays findable by order.
		_ = s.defaultResponse(request.Method, path, body)
	}
	if response.Block || s.isBlocked(request.Method, path) {
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
		case <-timer.C:
		}
		return
	}
	if response.Delay > 0 {
		time.Sleep(response.Delay)
	}
	s.writeResponse(writer, response)
}

func (s *Server) recordRequest(request Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
}

func (s *Server) nextResponse(method, path string) (Response, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range lookupKeys(method, path) {
		queued := s.responses[key]
		if len(queued) == 0 {
			continue
		}
		response := queued[0]
		s.responses[key] = queued[1:]
		return response, true
	}
	return Response{}, false
}

func (s *Server) isBlocked(method, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range lookupKeys(method, path) {
		if s.blocked[key] {
			return true
		}
	}
	return false
}

func (s *Server) defaultResponse(method, path string, body []byte) Response {
	switch {
	case method == http.MethodPost && path == "/checkout":
		return s.hostedPageResponse()
	case method == http.MethodPost && path == "/checkout-subscription":
		return s.hostedPageResponse()
	case method == http.MethodPost && path == "/checkout-schedule":
		return s.hostedPageResponse()
	case method == http.MethodPost && path == "/checkout-token":
		return Response{Code: successCode, Data: map[string]any{"id": s.checkoutID}}
	case method == http.MethodPost && path == "/token/transactions":
		hid, _, authCode := s.recordCharge(body, "onetime", "")
		return Response{Code: successCode, Data: map[string]any{"id": hid, "authCode": authCode}}
	case method == http.MethodPost && path == "/token/subscriptions":
		subscriptionID := s.newSubscription(body)
		hid, _, authCode := s.recordCharge(body, "subscription", subscriptionID)
		return Response{Code: successCode, Data: map[string]any{
			"subscriptionId": subscriptionID,
			"transactionId":  hid,
			"authCode":       authCode,
		}}
	case method == http.MethodPost && strings.HasPrefix(path, "/refunds/"):
		return s.refundResponse(strings.TrimPrefix(path, "/refunds/"), body)
	case method == http.MethodGet && path == "/transactions":
		return s.listResponse(s.allTransactions())
	case method == http.MethodGet && strings.HasPrefix(path, "/transactions/"):
		return s.transactionResponse(strings.TrimPrefix(path, "/transactions/"))
	case method == http.MethodGet && strings.HasPrefix(path, "/order/") && strings.HasSuffix(path, "/transactions"):
		orderID := strings.TrimSuffix(strings.TrimPrefix(path, "/order/"), "/transactions")
		return s.listResponse(s.orderTransactions(orderID))
	case method == http.MethodGet && strings.HasPrefix(path, "/subscriptions/"):
		return s.subscriptionResponse(strings.TrimPrefix(path, "/subscriptions/"), false, "")
	case method == http.MethodPut && strings.HasPrefix(path, "/subscriptions/"):
		var request struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(body, &request)
		return s.subscriptionResponse(strings.TrimPrefix(path, "/subscriptions/"), true, request.Reason)
	default:
		return Response{StatusCode: http.StatusNotFound}
	}
}

func (s *Server) hostedPageResponse() Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Response{Code: successCode, Data: map[string]any{
		"id":             s.checkoutID,
		"transactionHid": s.transactionHID,
	}}
}

func (s *Server) recordCharge(body []byte, action, subscriptionID string) (hid, transactionID, authCode string) {
	var request struct {
		OrderID string `json:"orderId"`
		Amount  int64  `json:"amount"`
	}
	_ = json.Unmarshal(body, &request)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.chargeCount++
	hid = s.transactionHID
	transactionID = s.transactionID
	if s.chargeCount > 1 {
		suffix := "-" + strconv.Itoa(s.chargeCount)
		hid += suffix
		transactionID += suffix
	}
	transaction := map[string]any{
		"id":            hid,
		"transactionId": transactionID,
		"action":        action,
		"amount":        request.Amount,
		"fee":           0,
		"status":        "charged",
		"orderId":       request.OrderID,
		"authCode":      s.authCode,
		"createdAt":     time.Now().UTC().Format(time.RFC3339Nano),
		"refundAmount":  0,
	}
	if s.cardNum != "" || s.cardType != "" {
		transaction["paymentInfo"] = map[string]any{
			"cardNum":  s.cardNum,
			"cardType": s.cardType,
			"method":   "card",
		}
	}
	if subscriptionID != "" {
		transaction["subscriptionId"] = subscriptionID
		transaction["period"] = 1
	}
	s.transactionsByID[hid] = cloneMap(transaction)
	s.transactionsByOrder[request.OrderID] = append(s.transactionsByOrder[request.OrderID], cloneMap(transaction))
	return hid, transactionID, s.authCode
}

func (s *Server) newSubscription(body []byte) string {
	var request struct {
		OrderID         string `json:"orderId"`
		Amount          int64  `json:"amount"`
		NumberOfPeriods int    `json:"numberOfPeriods"`
	}
	_ = json.Unmarshal(body, &request)

	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.subscriptionID
	if len(s.subscriptions) > 0 {
		id += "-" + strconv.Itoa(len(s.subscriptions)+1)
	}
	now := time.Now().UTC()
	s.subscriptions[id] = map[string]any{
		"id":              id,
		"status":          "ongoing",
		"amount":          request.Amount,
		"period":          1,
		"numberOfPeriods": request.NumberOfPeriods,
		"startedAt":       now.Format(time.RFC3339Nano),
		"createdAt":       now.Format(time.RFC3339Nano),
		"nextChargeAt":    now.AddDate(0, 1, 0).Format(time.RFC3339Nano),
		"orderId":         request.OrderID,
	}
	return id
}

func (s *Server) refundResponse(hid string, body []byte) Response {
	var request struct {
		Amount int64  `json:"amount"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &request)

	s.mu.Lock()
	defer s.mu.Unlock()
	transaction := s.transactionsByID[hid]
	if transaction == nil {
		return Response{Code: "V0002", Message: "transaction not found"}
	}
	transaction["status"] = "refunded"
	transaction["refundAmount"] = request.Amount
	transaction["refundedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	if request.Reason != "" {
		transaction["note"] = request.Reason
	}
	s.transactionsByID[hid] = transaction
	return Response{Code: successCode, Data: cloneMap(transaction)}
}

func (s *Server) transactionResponse(id string) Response {
	s.mu.Lock()
	transaction := cloneMap(s.transactionsByID[id])
	s.mu.Unlock()
	if transaction == nil {
		return Response{Code: "V0002", Message: "transaction not found"}
	}
	return Response{Code: successCode, Data: transaction}
}

func (s *Server) subscriptionResponse(id string, cancel bool, reason string) Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	subscription := s.subscriptions[id]
	if subscription == nil {
		return Response{Code: "V0002", Message: "subscription not found"}
	}
	if cancel {
		subscription["status"] = "cancelled"
		subscription["cancelledAt"] = time.Now().UTC().Format(time.RFC3339Nano)
		subscription["reason"] = reason
		delete(subscription, "nextChargeAt")
		s.subscriptions[id] = subscription
	}
	return Response{Code: successCode, Data: cloneMap(subscription)}
}

func (s *Server) orderTransactions(orderID string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.transactionsByOrder[orderID]
	transactions := make([]map[string]any, 0, len(stored))
	for _, transaction := range stored {
		// Serve the current state of the transaction, not the state it had
		// when the charge landed.
		if current := s.transactionsByID[stringField(transaction, "id")]; current != nil {
			transaction = current
		}
		transactions = append(transactions, cloneMap(transaction))
	}
	return transactions
}

func (s *Server) allTransactions() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	transactions := make([]map[string]any, 0, len(s.transactionsByID))
	for _, transaction := range s.transactionsByID {
		transactions = append(transactions, cloneMap(transaction))
	}
	return transactions
}

func (s *Server) listResponse(transactions []map[string]any) Response {
	data := map[string]any{"transactions": transactions}
	s.mu.Lock()
	token := s.nextPageToken
	s.mu.Unlock()
	if token != "" {
		data["page"] = token
	}
	return Response{Code: successCode, Data: data}
}

func (s *Server) writeResponse(writer http.ResponseWriter, response Response) {
	for key, values := range response.Headers {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	status := response.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	writer.WriteHeader(status)
	if response.Body != nil {
		_, _ = writer.Write(response.Body)
		return
	}
	code := response.Code
	if code == "" {
		code = successCode
	}
	envelope := map[string]any{"code": code, "message": response.Message}
	if response.Data != nil {
		envelope["data"] = response.Data
	}
	_ = json.NewEncoder(writer).Encode(envelope)
}

func lookupKeys(method, path string) []string {
	return []string{
		method + " " + path,
		path,
		method + " " + routeKey(path),
		routeKey(path),
	}
}

func normalizeKey(endpoint string) string {
	method, path, found := strings.Cut(endpoint, " ")
	if found {
		return strings.ToUpper(method) + " " + normalizePath(path)
	}
	return normalizePath(endpoint)
}

func normalizePath(endpoint string) string {
	if i := strings.IndexByte(endpoint, '?'); i >= 0 {
		endpoint = endpoint[:i]
	}
	if endpoint == "" {
		return "/"
	}
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}
	if endpoint == "/" {
		return endpoint
	}
	return strings.TrimSuffix(endpoint, "/")
}

func routeKey(path string) string {
	switch {
	case strings.HasPrefix(path, "/order/") && strings.HasSuffix(path, "/transactions"):
		return "/order/{orderId}/transactions"
	case strings.HasPrefix(path, "/transactions/"):
		return "/transactions/{id}"
	case strings.HasPrefix(path, "/subscriptions/"):
		return "/subscriptions/{subscriptionId}"
	case strings.HasPrefix(path, "/refunds/"):
		return "/refunds/{transactionHid}"
	default:
		return path
	}
}

func cloneRequest(request Request) Request {
	request.Header = request.Header.Clone()
	request.Body = append([]byte(nil), request.Body...)
	return request
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func stringField(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}
