package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name  string
		cfg   Config
		field string
	}{
		{"missing key", Config{}, "apiKey"},
		{"payment API token", Config{APIKey: "abc"}, "apiKey"},
		{"relative base", Config{APIKey: testKey, BaseURL: "subscription-api.oen.tw"}, "baseURL"},
		{"ftp base", Config{APIKey: testKey, BaseURL: "ftp://example.com"}, "baseURL"},
		{"query in base", Config{APIKey: testKey, BaseURL: "https://example.com/?x=1"}, "baseURL"},
		{"negative timeout", Config{APIKey: testKey, Timeout: -time.Second}, "timeout"},
		{"control character in key", Config{APIKey: testKey + "\nX-Injected: 1"}, "apiKey"},
		{"control character in user agent", Config{APIKey: testKey, UserAgent: "app\r\n"}, "userAgent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			errIs(t, err, ErrInvalidInput)
			equal(t, tc.field, fieldOf(t, err))
			notContains(t, err.Error(), testKey)
		})
	}
}

func TestConfigDefaultsToTheProductionHost(t *testing.T) {
	client, err := New(Config{APIKey: "  " + testKey + " "})
	noError(t, err)
	equal(t, DefaultBaseURL, client.BaseURL())
	equal(t, testKey, client.cfg.APIKey)
	equal(t, defaultTimeout, client.cfg.Timeout)

	client, err = New(Config{APIKey: testKey, BaseURL: "http://localhost:8080/prefix/"})
	noError(t, err)
	equal(t, "http://localhost:8080/prefix", client.BaseURL())
}

func TestNewDoesNotChangeTheSuppliedHTTPClient(t *testing.T) {
	supplied := &http.Client{Timeout: time.Second}
	_, err := New(Config{APIKey: testKey, HTTPClient: supplied})
	noError(t, err)
	isTrue(t, supplied.CheckRedirect == nil)
}

func TestCreateProductSendsEveryDocumentedField(t *testing.T) {
	f, client := newFake(t, http.StatusOK, `{"data":{"id":"prod_1","name":"Pro"}}`)
	grace := 3
	product, err := client.CreateProduct(context.Background(), CreateProductRequest{
		Name:                 " 專業版 ",
		SubscriptionType:     SubscriptionTypeTiered,
		Status:               "inactive",
		Summary:              "summary",
		Description:          "description",
		TrialReuse:           TrialReuseOncePerProduct,
		GracePeriodDays:      &grace,
		BasicInfoFields:      json.RawMessage(`{"phone":true}`),
		WebsiteURL:           "https://shop.example",
		SuccessRedirectURL:   "https://shop.example/ok",
		FailureRedirectURL:   "https://shop.example/ng",
		CustomerServicePhone: "02-1234-5678",
		CustomerServiceEmail: "cs@shop.example",
	})
	noError(t, err)
	equal(t, "prod_1", product.ID)
	jsonEqual(t, `{"id":"prod_1","name":"Pro"}`, string(product.Raw))
	jsonEqual(t, `{
		"name":"專業版","subscriptionType":"tiered","status":"inactive",
		"summary":"summary","description":"description","trialReuse":"once_per_product",
		"gracePeriodDays":3,"basicInfoFields":{"phone":true},
		"websiteUrl":"https://shop.example","successRedirectUrl":"https://shop.example/ok",
		"failureRedirectUrl":"https://shop.example/ng",
		"customerServicePhone":"02-1234-5678","customerServiceEmail":"cs@shop.example"
	}`, f.last(t).Body)
}

func TestCreatePlanSendsZeroPriceAndTrialDays(t *testing.T) {
	f, client := newFake(t, http.StatusOK, `{"data":{"id":"plan_1"}}`)
	trial := 0
	plan, err := client.CreatePlan(context.Background(), CreatePlanRequest{
		ProductID: "prod_1", Name: "免費", Price: 0, TrialDays: &trial,
		BillingPeriod: BillingPeriod{Unit: PeriodYear, Interval: 2},
		Description:   "d", Status: "active",
	})
	noError(t, err)
	equal(t, "plan_1", plan.ID)
	jsonEqual(t, `{"name":"免費","price":0,"trialDays":0,"billingPeriod":{"unit":"year","interval":2},"description":"d","status":"active"}`,
		f.last(t).Body)
}

func TestRequestsAreValidatedBeforeSending(t *testing.T) {
	product := func(edit func(*CreateProductRequest)) CreateProductRequest {
		req := CreateProductRequest{Name: "Pro", SubscriptionType: SubscriptionTypeFixedPeriod}
		edit(&req)
		return req
	}
	plan := func(edit func(*CreatePlanRequest)) CreatePlanRequest {
		req := CreatePlanRequest{ProductID: "prod_1", Name: "M", Price: 1, BillingPeriod: BillingPeriod{Unit: PeriodDay, Interval: 1}}
		edit(&req)
		return req
	}
	ctx := context.Background()
	tests := []struct {
		name  string
		field string
		call  func(*Client) error
	}{
		{"product name missing", "name", func(c *Client) error {
			_, err := c.CreateProduct(ctx, product(func(r *CreateProductRequest) { r.Name = " " }))
			return err
		}},
		{"product name 51 characters", "name", func(c *Client) error {
			_, err := c.CreateProduct(ctx, product(func(r *CreateProductRequest) { r.Name = strings.Repeat("訂", 51) }))
			return err
		}},
		{"product type unknown", "subscriptionType", func(c *Client) error {
			_, err := c.CreateProduct(ctx, product(func(r *CreateProductRequest) { r.SubscriptionType = "monthly" }))
			return err
		}},
		{"product summary 101 characters", "summary", func(c *Client) error {
			_, err := c.CreateProduct(ctx, product(func(r *CreateProductRequest) { r.Summary = strings.Repeat("a", 101) }))
			return err
		}},
		{"product trial reuse unknown", "trialReuse", func(c *Client) error {
			_, err := c.CreateProduct(ctx, product(func(r *CreateProductRequest) { r.TrialReuse = "twice" }))
			return err
		}},
		{"product http redirect", "successRedirectUrl", func(c *Client) error {
			_, err := c.CreateProduct(ctx, product(func(r *CreateProductRequest) { r.SuccessRedirectURL = "http://shop.example" }))
			return err
		}},
		{"product invalid basic info", "basicInfoFields", func(c *Client) error {
			_, err := c.CreateProduct(ctx, product(func(r *CreateProductRequest) { r.BasicInfoFields = json.RawMessage(`{`) }))
			return err
		}},
		{"plan name empty", "name", func(c *Client) error {
			_, err := c.CreatePlan(ctx, plan(func(r *CreatePlanRequest) { r.Name = "" }))
			return err
		}},
		{"plan name 31 characters", "name", func(c *Client) error {
			_, err := c.CreatePlan(ctx, plan(func(r *CreatePlanRequest) { r.Name = strings.Repeat("方", 31) }))
			return err
		}},
		{"plan negative price", "price", func(c *Client) error {
			_, err := c.CreatePlan(ctx, plan(func(r *CreatePlanRequest) { r.Price = -1 }))
			return err
		}},
		{"plan unit unknown", "billingPeriod.unit", func(c *Client) error {
			_, err := c.CreatePlan(ctx, plan(func(r *CreatePlanRequest) { r.BillingPeriod.Unit = "week" }))
			return err
		}},
		{"plan interval zero", "billingPeriod.interval", func(c *Client) error {
			_, err := c.CreatePlan(ctx, plan(func(r *CreatePlanRequest) { r.BillingPeriod.Interval = 0 }))
			return err
		}},
		{"plan description 1001 characters", "description", func(c *Client) error {
			_, err := c.CreatePlan(ctx, plan(func(r *CreatePlanRequest) { r.Description = strings.Repeat("a", 1001) }))
			return err
		}},
		{"plan product missing", "productId", func(c *Client) error {
			_, err := c.CreatePlan(ctx, plan(func(r *CreatePlanRequest) { r.ProductID = "" }))
			return err
		}},
		{"update product without fields", "fields", func(c *Client) error {
			_, err := c.UpdateProduct(ctx, UpdateProductRequest{ProductID: "prod_1"})
			return err
		}},
		{"update plan without plan", "planId", func(c *Client) error {
			_, err := c.UpdatePlan(ctx, UpdatePlanRequest{ProductID: "prod_1", Fields: map[string]any{"a": 1}})
			return err
		}},
		{"update customer without fields", "fields", func(c *Client) error {
			_, err := c.UpdateCustomer(ctx, UpdateCustomerRequest{CustomerID: "cus_1"})
			return err
		}},
		{"change plan without fields", "fields", func(c *Client) error {
			_, err := c.ChangePlan(ctx, KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
			return err
		}},
		{"change period without fields", "fields", func(c *Client) error {
			_, err := c.ChangePeriod(ctx, KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k"})
			return err
		}},
		{"key with newline", "idempotencyKey", func(c *Client) error {
			_, err := c.CancelSubscription(ctx, KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: "k\nX-Evil: 1"})
			return err
		}},
		{"key with padding", "idempotencyKey", func(c *Client) error {
			_, err := c.CancelSubscription(ctx, KeyedRequest{SubscriptionID: "sub_1", IdempotencyKey: " k"})
			return err
		}},
		{"subscription missing", "subscriptionId", func(c *Client) error {
			_, err := c.CancelSubscription(ctx, KeyedRequest{IdempotencyKey: "k"})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, client := newFake(t, http.StatusOK, `{"data":{}}`)
			err := tc.call(client)
			errIs(t, err, ErrInvalidInput)
			equal(t, tc.field, fieldOf(t, err))
			equal(t, 0, f.count())
		})
	}
}

func TestLimitsCountCharactersNotBytes(t *testing.T) {
	_, client := newFake(t, http.StatusOK, `{"data":{"id":"prod_1"}}`)
	_, err := client.CreateProduct(context.Background(), CreateProductRequest{
		Name: strings.Repeat("訂", 50), SubscriptionType: SubscriptionTypeFixedPeriod,
		Summary: strings.Repeat("閱", 100),
	})
	noError(t, err)
}
