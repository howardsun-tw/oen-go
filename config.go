package oen

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

// Environment selects the Oen hosts. There is no default: an integration that
// guesses would send live charges to the wrong place.
type Environment string

const (
	Production Environment = "production"
	Testing    Environment = "testing"
)

// Config describes one merchant's access to Oen.
type Config struct {
	// Environment fills BaseURL and CheckoutBaseURL when they are empty.
	Environment Environment

	// BaseURL overrides the API host, for example a local fake. A path prefix
	// is allowed, but query strings and fragments are not.
	BaseURL string

	// CheckoutBaseURL is the host that serves the hosted payment pages,
	// https://{merchantId}.oen.tw in production. Redirect URLs are built from
	// it; leaving it empty leaves [CheckoutSession.RedirectURL] empty too.
	// A path prefix is allowed, but query strings and fragments are not.
	CheckoutBaseURL string

	// MerchantID is the merchant's Oen domain name, for example "oentech" for
	// https://oentech.oen.tw. It is sent in every request body.
	MerchantID string

	// AuthToken is the bearer token from the Oen CRM console. It is never
	// logged and never returned in an error.
	AuthToken string

	// Timeout bounds one HTTP request. It defaults to 10 seconds. A timeout on
	// a charge does not mean the charge failed; see [ErrUnknownOutcome].
	Timeout time.Duration

	// HTTPClient is used for every request. A caller that needs proxies, mTLS
	// or connection tuning supplies its own. The SDK copies this client and
	// disables redirects without changing the supplied client. Custom
	// transports must not retry state-changing requests.
	HTTPClient *http.Client

	// UserAgent is sent with every request when set.
	UserAgent string

	// DefaultSuccessURL and DefaultFailureURL fill the return URLs of hosted
	// page requests that leave them empty. When set they must be absolute
	// http or https URLs.
	DefaultSuccessURL string
	DefaultFailureURL string

	// NaiveTimeLocation interprets timestamps that arrive without an offset.
	// Oen documents every date as ISO in UTC+0, so it defaults to time.UTC.
	NaiveTimeLocation *time.Location

	// Now supplies the clock used for Retry-After deadlines and call duration.
	Now func() time.Time

	// Logf receives one line per request: method, path, HTTP status, outcome
	// and duration. It never receives credentials, tokens or card data.
	Logf func(format string, args ...any)
}

func (cfg Config) normalized() (Config, error) {
	const op = "Config"

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.CheckoutBaseURL = strings.TrimRight(strings.TrimSpace(cfg.CheckoutBaseURL), "/")
	cfg.MerchantID = strings.TrimSpace(cfg.MerchantID)
	cfg.AuthToken = strings.TrimSpace(cfg.AuthToken)
	cfg.DefaultSuccessURL = strings.TrimSpace(cfg.DefaultSuccessURL)
	cfg.DefaultFailureURL = strings.TrimSpace(cfg.DefaultFailureURL)

	if cfg.MerchantID == "" {
		return Config{}, newValidationError(op, "merchantId", "merchant ID is required")
	}
	if err := validateMerchantID(op, cfg.MerchantID); err != nil {
		return Config{}, err
	}
	if cfg.AuthToken == "" {
		return Config{}, newValidationError(op, "authToken", "auth token is required")
	}

	switch cfg.Environment {
	case Production, Testing:
		apiHost, checkoutHost := environmentHosts(cfg.Environment, cfg.MerchantID)
		if cfg.BaseURL == "" {
			cfg.BaseURL = apiHost
		}
		if cfg.CheckoutBaseURL == "" {
			cfg.CheckoutBaseURL = checkoutHost
		}
	case "":
		if cfg.BaseURL == "" {
			return Config{}, newValidationError(op, "environment",
				"set Environment to production or testing, or set BaseURL explicitly")
		}
	default:
		return Config{}, newValidationError(op, "environment",
			fmt.Sprintf("unknown environment %q", cfg.Environment))
	}

	if err := validateBaseURL(op, "baseURL", cfg.BaseURL); err != nil {
		return Config{}, err
	}
	if cfg.CheckoutBaseURL != "" {
		if err := validateBaseURL(op, "checkoutBaseURL", cfg.CheckoutBaseURL); err != nil {
			return Config{}, err
		}
	}
	if cfg.DefaultSuccessURL != "" {
		if err := validateHTTPURL(op, "defaultSuccessURL", cfg.DefaultSuccessURL); err != nil {
			return Config{}, err
		}
	}
	if cfg.DefaultFailureURL != "" {
		if err := validateHTTPURL(op, "defaultFailureURL", cfg.DefaultFailureURL); err != nil {
			return Config{}, err
		}
	}

	if cfg.Timeout < 0 {
		return Config{}, newValidationError(op, "timeout", "timeout must not be negative")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.NaiveTimeLocation == nil {
		cfg.NaiveTimeLocation = time.UTC
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return cfg, nil
}

func environmentHosts(environment Environment, merchantID string) (apiHost, checkoutHost string) {
	if environment == Testing {
		return "https://payment-api.testing.oen.tw", "https://" + merchantID + ".testing.oen.tw"
	}
	return "https://payment-api.oen.tw", "https://" + merchantID + ".oen.tw"
}

// validateMerchantID requires a single DNS label. Checkout hosts are built as
// https://{merchantId}.oen.tw; values with "/", "@" or dots would point the
// payer at the wrong host.
func validateMerchantID(op, merchantID string) error {
	if len(merchantID) > 63 {
		return newValidationError(op, "merchantId", "merchant ID must be a single DNS label (at most 63 characters)")
	}
	for i, r := range merchantID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			continue
		case r == '-' && i > 0 && i < len(merchantID)-1:
			continue
		default:
			return newValidationError(op, "merchantId", "merchant ID must be a single DNS label")
		}
	}
	return nil
}

func validateBaseURL(op, field, value string) error {
	if err := validateHTTPURL(op, field, value); err != nil {
		return err
	}
	// Literal delimiters would swallow an appended endpoint path. Escaped
	// delimiters in a path prefix are valid and remain untouched.
	if strings.ContainsAny(value, "?#") {
		return newValidationError(op, field, "base URL must not contain a query string or fragment")
	}
	return nil
}

func validateHTTPURL(op, field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return newValidationError(op, field, fmt.Sprintf("%q is not an absolute URL", value))
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return newValidationError(op, field, fmt.Sprintf("%q must use http or https", value))
	}
	return nil
}
