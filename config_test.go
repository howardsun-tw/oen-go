package oen

import (
	"net/http"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		Environment: Testing,
		MerchantID:  "oentech",
		AuthToken:   "auth-token",
	}
}

func TestNewRejectsBaseURLsThatCannotAppendEndpointPaths(t *testing.T) {
	for _, field := range []string{"baseURL", "checkoutBaseURL"} {
		for _, suffix := range []string{"?tenant=one", "?", "#section", "#"} {
			t.Run(field+suffix, func(t *testing.T) {
				cfg := validConfig()
				value := "https://example.com/api" + suffix
				if field == "baseURL" {
					cfg.BaseURL = value
				} else {
					cfg.CheckoutBaseURL = value
				}
				_, err := New(cfg)
				errIs(t, err, ErrInvalidInput)
				var detail *ValidationError
				isTrue(t, asValidationError(err, &detail))
				equal(t, field, detail.Field)
			})
		}
	}
}

func TestNewPreservesBasePathPrefixesAndReturnURLQueries(t *testing.T) {
	cfg := validConfig()
	cfg.BaseURL = "https://api.example/v1%3Fpart%23name"
	cfg.CheckoutBaseURL = "https://checkout.example/payments"
	cfg.DefaultSuccessURL = "https://merchant.example/return?order=one#paid"
	cfg.DefaultFailureURL = "https://merchant.example/return?order=one#failed"
	client, err := New(cfg)
	noError(t, err)
	equal(t, cfg.BaseURL, client.BaseURL())
	equal(t, cfg.CheckoutBaseURL, client.CheckoutBaseURL())
}

// Oen publishes one API host and one checkout host per environment, and the
// checkout host carries the merchant name. Deriving both from the environment
// is the difference between a working integration and a production charge sent
// to the sandbox.
func TestConfigDerivesTheDocumentedHosts(t *testing.T) {
	testing_, err := validConfig().normalized()
	noError(t, err)
	equal(t, "https://payment-api.testing.oen.tw", testing_.BaseURL)
	equal(t, "https://oentech.testing.oen.tw", testing_.CheckoutBaseURL)

	production, err := Config{Environment: Production, MerchantID: "oentech", AuthToken: "auth-token"}.normalized()
	noError(t, err)
	equal(t, "https://payment-api.oen.tw", production.BaseURL)
	equal(t, "https://oentech.oen.tw", production.CheckoutBaseURL)
}

func TestConfigKeepsExplicitURLsAndTrimsTrailingSlashes(t *testing.T) {
	cfg := validConfig()
	cfg.BaseURL = "https://payment-api.example/ "
	cfg.CheckoutBaseURL = "https://checkout.example/"
	normalized, err := cfg.normalized()
	noError(t, err)
	equal(t, "https://payment-api.example", normalized.BaseURL)
	equal(t, "https://checkout.example", normalized.CheckoutBaseURL)
}

// Without an environment and without an explicit host there is no safe
// default: guessing production would send real charges.
func TestConfigRefusesToGuessTheEnvironment(t *testing.T) {
	cfg := validConfig()
	cfg.Environment = ""
	_, err := cfg.normalized()
	hasError(t, err)
	errIs(t, err, ErrInvalidInput)
	contains(t, err.Error(), "environment")

	cfg.BaseURL = "https://payment-api.example"
	normalized, err := cfg.normalized()
	noError(t, err)
	equal(t, "https://payment-api.example", normalized.BaseURL)
	// The checkout host still has no documented default without an environment.
	equal(t, "", normalized.CheckoutBaseURL)
}

func TestConfigValidatesEveryRequiredSetting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{name: "unknown environment", mutate: func(c *Config) { c.Environment = "staging" }, field: "environment"},
		{name: "missing merchant", mutate: func(c *Config) { c.MerchantID = " " }, field: "merchantId"},
		{name: "merchant with slash", mutate: func(c *Config) { c.MerchantID = "evil.com/x" }, field: "merchantId"},
		{name: "merchant with at", mutate: func(c *Config) { c.MerchantID = "foo@bar" }, field: "merchantId"},
		{name: "merchant with dot", mutate: func(c *Config) { c.MerchantID = "oen.tw" }, field: "merchantId"},
		{name: "missing token", mutate: func(c *Config) { c.AuthToken = "" }, field: "authToken"},
		{name: "negative timeout", mutate: func(c *Config) { c.Timeout = -time.Second }, field: "timeout"},
		{name: "unparseable base URL", mutate: func(c *Config) { c.BaseURL = "://nope" }, field: "baseURL"},
		{name: "relative base URL", mutate: func(c *Config) { c.BaseURL = "payment-api.oen.tw" }, field: "baseURL"},
		{name: "non http scheme", mutate: func(c *Config) { c.BaseURL = "ftp://payment-api.oen.tw" }, field: "baseURL"},
		{name: "bad checkout URL", mutate: func(c *Config) { c.CheckoutBaseURL = "nope" }, field: "checkoutBaseURL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.mutate(&cfg)
			_, err := cfg.normalized()
			hasError(t, err)
			errIs(t, err, ErrInvalidInput)
			var target *ValidationError
			isTrue(t, asValidationError(err, &target))
			equal(t, test.field, target.Field)
		})
	}
}

func TestConfigFillsRuntimeDefaults(t *testing.T) {
	normalized, err := validConfig().normalized()
	noError(t, err)
	equal(t, defaultTimeout, normalized.Timeout)
	equal(t, time.UTC, normalized.NaiveTimeLocation)

	explicit := validConfig()
	explicit.Timeout = 3 * time.Second
	explicit.NaiveTimeLocation = time.FixedZone("UTC+8", 8*60*60)
	normalized, err = explicit.normalized()
	noError(t, err)
	equal(t, 3*time.Second, normalized.Timeout)
	equal(t, "UTC+8", normalized.NaiveTimeLocation.String())
}

func TestNewRejectsBadConfigAndAcceptsGoodOne(t *testing.T) {
	_, err := New(Config{})
	hasError(t, err)
	errIs(t, err, ErrInvalidInput)

	client, err := New(validConfig())
	noError(t, err)
	equal(t, "oentech", client.MerchantID())
	equal(t, Testing, client.Environment())
	equal(t, "https://payment-api.testing.oen.tw", client.BaseURL())
	equal(t, "https://oentech.testing.oen.tw", client.CheckoutBaseURL())

	custom := validConfig()
	custom.HTTPClient = &http.Client{Timeout: time.Minute}
	client, err = New(custom)
	noError(t, err)
	equal(t, time.Minute, client.httpClient.Timeout)
	isTrue(t, custom.HTTPClient.CheckRedirect == nil)
	isTrue(t, custom.HTTPClient != client.httpClient)
}
