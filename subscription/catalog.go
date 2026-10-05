package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"
)

// SubscriptionType is how a product's plans relate. It cannot change after
// the product is created.
type SubscriptionType string

const (
	SubscriptionTypeFixedPeriod SubscriptionType = "fixedPeriod"
	// SubscriptionTypeTiered products charge the difference through a
	// payment link when a subscriber upgrades.
	SubscriptionTypeTiered SubscriptionType = "tiered"
)

// TrialReuse limits how often one customer may start a trial.
type TrialReuse string

const (
	TrialReuseUnlimited      TrialReuse = "unlimited"
	TrialReuseOncePerPlan    TrialReuse = "once_per_plan"
	TrialReuseOncePerProduct TrialReuse = "once_per_product"
)

// PeriodUnit is the unit of a plan's billing period.
type PeriodUnit string

const (
	PeriodDay   PeriodUnit = "day"
	PeriodMonth PeriodUnit = "month"
	PeriodYear  PeriodUnit = "year"
)

// BillingPeriod is how often a plan charges, such as every 3 months. It
// cannot change after the plan is created.
type BillingPeriod struct {
	Unit     PeriodUnit `json:"unit"`
	Interval int        `json:"interval"`
}

// CreateProductRequest is POST /v1/products (建立產品). Empty optional fields
// are not sent.
type CreateProductRequest struct {
	// Name is required, at most 50 characters.
	Name string
	// SubscriptionType is required.
	SubscriptionType SubscriptionType
	// Status defaults to inactive on Oen's side.
	Status string
	// Summary is at most 100 characters.
	Summary     string
	Description string
	TrialReuse  TrialReuse
	// GracePeriodDays is sent when not nil.
	GracePeriodDays *int
	// BasicInfoFields controls whether the subscription page collects a
	// phone number and an address, and whether it accepts overseas
	// addresses. Oen does not document its shape, so it is sent verbatim.
	BasicInfoFields json.RawMessage
	// WebsiteURL, SuccessRedirectURL and FailureRedirectURL must be https.
	WebsiteURL           string
	SuccessRedirectURL   string
	FailureRedirectURL   string
	CustomerServicePhone string
	CustomerServiceEmail string
}

// UpdateProductRequest is PUT /v1/products/{productId} (更新產品). Oen does
// not document the update fields, so Fields is sent as the JSON body.
type UpdateProductRequest struct {
	ProductID string
	Fields    map[string]any
}

// CreatePlanRequest is POST /v1/products/{productId}/plans (建立方案).
type CreatePlanRequest struct {
	ProductID string
	// Name is required, 1 to 30 characters.
	Name string
	// Price is a whole amount, 0 or more. It is always sent.
	Price int64
	// BillingPeriod is required.
	BillingPeriod BillingPeriod
	// TrialDays is sent when not nil; otherwise the product's setting applies.
	TrialDays *int
	// Description is at most 1,000 characters.
	Description string
	// Status defaults to active on Oen's side.
	Status string
}

// UpdatePlanRequest is PUT /v1/products/{productId}/plans/{planId} (更新方案).
// Oen does not document the update fields, so Fields is sent as the JSON
// body.
type UpdatePlanRequest struct {
	ProductID string
	PlanID    string
	Fields    map[string]any
}

// CreateProduct creates a product. It takes no Idempotency-Key, so an
// unknown outcome may mean the product exists; list products before creating
// it again.
func (c *Client) CreateProduct(ctx context.Context, req CreateProductRequest) (*Product, error) {
	const op = "CreateProduct"
	body, err := productBody(op, req)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodPost, path: "/v1/products", body: body}, decodeProduct)
}

// ListProducts reads one page of products. Pass "" for the first page and
// the previous Page.Next afterwards.
func (c *Client) ListProducts(ctx context.Context, page string) (*Page[Product], error) {
	return list(ctx, c, request{op: "ListProducts", method: http.MethodGet, path: "/v1/products", query: pageQuery(page)}, decodeProduct)
}

// GetProduct reads one product.
func (c *Client) GetProduct(ctx context.Context, productID string) (*Product, error) {
	const op = "GetProduct"
	id, err := pathSegment(op, "productId", productID)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodGet, path: "/v1/products/" + id}, decodeProduct)
}

// UpdateProduct changes a product. SubscriptionType cannot change.
func (c *Client) UpdateProduct(ctx context.Context, req UpdateProductRequest) (*Product, error) {
	const op = "UpdateProduct"
	id, err := pathSegment(op, "productId", req.ProductID)
	if err != nil {
		return nil, err
	}
	if err := requireFields(op, req.Fields); err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodPut, path: "/v1/products/" + id, body: req.Fields}, decodeProduct)
}

// GetSubscriptionURL reads the URL of the product's subscription page, which
// every subscriber shares. Oen does not document the response shape.
func (c *Client) GetSubscriptionURL(ctx context.Context, productID string) (*Result, error) {
	const op = "GetSubscriptionURL"
	id, err := pathSegment(op, "productId", productID)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodGet, path: "/v1/products/" + id + "/subscription-url"}, decodeResult)
}

// CreatePlan creates a plan. It takes no Idempotency-Key, so an unknown
// outcome may mean the plan exists; list plans before creating it again.
func (c *Client) CreatePlan(ctx context.Context, req CreatePlanRequest) (*Plan, error) {
	const op = "CreatePlan"
	id, err := pathSegment(op, "productId", req.ProductID)
	if err != nil {
		return nil, err
	}
	body, err := planBody(op, req)
	if err != nil {
		return nil, err
	}
	return call(ctx, c, request{op: op, method: http.MethodPost, path: "/v1/products/" + id + "/plans", body: body}, decodePlan)
}

// ListPlans reads one page of a product's plans.
func (c *Client) ListPlans(ctx context.Context, productID, page string) (*Page[Plan], error) {
	const op = "ListPlans"
	id, err := pathSegment(op, "productId", productID)
	if err != nil {
		return nil, err
	}
	return list(ctx, c, request{op: op, method: http.MethodGet, path: "/v1/products/" + id + "/plans", query: pageQuery(page)}, decodePlan)
}

// UpdatePlan changes a plan. BillingPeriod cannot change.
func (c *Client) UpdatePlan(ctx context.Context, req UpdatePlanRequest) (*Plan, error) {
	const op = "UpdatePlan"
	productID, err := pathSegment(op, "productId", req.ProductID)
	if err != nil {
		return nil, err
	}
	planID, err := pathSegment(op, "planId", req.PlanID)
	if err != nil {
		return nil, err
	}
	if err := requireFields(op, req.Fields); err != nil {
		return nil, err
	}
	return call(ctx, c, request{
		op: op, method: http.MethodPut,
		path: "/v1/products/" + productID + "/plans/" + planID, body: req.Fields,
	}, decodePlan)
}

func productBody(op string, req CreateProductRequest) (map[string]any, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, newValidationError(op, "name", "name is required")
	}
	if err := maxRunes(op, "name", name, 50); err != nil {
		return nil, err
	}
	switch req.SubscriptionType {
	case SubscriptionTypeFixedPeriod, SubscriptionTypeTiered:
	default:
		return nil, newValidationError(op, "subscriptionType", "subscriptionType must be fixedPeriod or tiered")
	}
	if err := maxRunes(op, "summary", req.Summary, 100); err != nil {
		return nil, err
	}
	switch req.TrialReuse {
	case "", TrialReuseUnlimited, TrialReuseOncePerPlan, TrialReuseOncePerProduct:
	default:
		return nil, newValidationError(op, "trialReuse", "trialReuse must be unlimited, once_per_plan or once_per_product")
	}
	for field, value := range map[string]string{
		"websiteUrl":         req.WebsiteURL,
		"successRedirectUrl": req.SuccessRedirectURL,
		"failureRedirectUrl": req.FailureRedirectURL,
	} {
		if err := httpsURL(op, field, value); err != nil {
			return nil, err
		}
	}
	if len(req.BasicInfoFields) > 0 && !json.Valid(req.BasicInfoFields) {
		return nil, newValidationError(op, "basicInfoFields", "basicInfoFields must be valid JSON")
	}

	body := map[string]any{"name": name, "subscriptionType": req.SubscriptionType}
	setString(body, "status", req.Status)
	setString(body, "summary", req.Summary)
	setString(body, "description", req.Description)
	setString(body, "trialReuse", string(req.TrialReuse))
	setString(body, "websiteUrl", req.WebsiteURL)
	setString(body, "successRedirectUrl", req.SuccessRedirectURL)
	setString(body, "failureRedirectUrl", req.FailureRedirectURL)
	setString(body, "customerServicePhone", req.CustomerServicePhone)
	setString(body, "customerServiceEmail", req.CustomerServiceEmail)
	if req.GracePeriodDays != nil {
		body["gracePeriodDays"] = *req.GracePeriodDays
	}
	if len(req.BasicInfoFields) > 0 {
		body["basicInfoFields"] = req.BasicInfoFields
	}
	return body, nil
}

func planBody(op string, req CreatePlanRequest) (map[string]any, error) {
	name := strings.TrimSpace(req.Name)
	if count := utf8.RuneCountInString(name); count < 1 || count > 30 {
		return nil, newValidationError(op, "name", "name must be 1 to 30 characters")
	}
	if req.Price < 0 {
		return nil, newValidationError(op, "price", "price must be 0 or more")
	}
	switch req.BillingPeriod.Unit {
	case PeriodDay, PeriodMonth, PeriodYear:
	default:
		return nil, newValidationError(op, "billingPeriod.unit", "billingPeriod.unit must be day, month or year")
	}
	if req.BillingPeriod.Interval < 1 {
		return nil, newValidationError(op, "billingPeriod.interval", "billingPeriod.interval must be 1 or more")
	}
	if err := maxRunes(op, "description", req.Description, 1000); err != nil {
		return nil, err
	}

	body := map[string]any{"name": name, "price": req.Price, "billingPeriod": req.BillingPeriod}
	if req.TrialDays != nil {
		body["trialDays"] = *req.TrialDays
	}
	setString(body, "description", req.Description)
	setString(body, "status", req.Status)
	return body, nil
}

func setString(body map[string]any, key, value string) {
	if value != "" {
		body[key] = value
	}
}
