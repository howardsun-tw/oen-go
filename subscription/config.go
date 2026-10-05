package subscription

import (
	"net/http"
	"strings"
	"time"

	"github.com/howardsun-tw/oen-go/internal/httpx"
)

// DefaultBaseURL is the only Subscription API host. Oen offers no public
// testing environment for this product.
const DefaultBaseURL = "https://subscription-api.oen.tw"

const (
	defaultTimeout = 10 * time.Second
	apiKeyPrefix   = "sub_sk_"
)

// Config describes one merchant's access to the Subscription API.
type Config struct {
	// APIKey is a Subscription API key (sub_sk_…) created in the CRM under
	// 訂閱管理 → 設定 → API 金鑰. A Payment API token is not accepted. The key
	// is never logged and never returned in an error.
	APIKey string

	// BaseURL overrides DefaultBaseURL, for example to point at a local fake.
	// Endpoint paths, which start with /v1, are appended to it. A path prefix
	// is allowed, but query strings and fragments are not.
	BaseURL string

	// Timeout bounds one HTTP request. It defaults to 10 seconds. A timeout
	// on a write does not mean the write failed; see [ErrUnknownOutcome].
	Timeout time.Duration

	// HTTPClient is used for every request. The SDK copies it and disables
	// redirects without changing the supplied client. Custom transports must
	// not retry requests.
	HTTPClient *http.Client

	// UserAgent is sent with every request when set.
	UserAgent string

	// Now supplies the clock used for Retry-After deadlines and call duration.
	Now func() time.Time

	// Logf receives one line per request: method, path, HTTP status, outcome
	// and duration. It never receives the API key or a payload.
	Logf func(format string, args ...any)
}

func (cfg Config) normalized() (Config, error) {
	const op = "Config"

	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")

	if cfg.APIKey == "" {
		return Config{}, newValidationError(op, "apiKey", "API key is required")
	}
	if !strings.HasPrefix(cfg.APIKey, apiKeyPrefix) {
		return Config{}, newValidationError(op, "apiKey", "API key must start with "+apiKeyPrefix)
	}
	// net/http refuses to send a header value with control characters, and
	// that refusal surfaces as a transport failure, which would read as an
	// unknown outcome for a request that never left the process.
	if !httpx.PrintableASCII(cfg.APIKey) {
		return Config{}, newValidationError(op, "apiKey", "API key must be printable ASCII")
	}
	if httpx.HasControlChars(cfg.UserAgent) {
		return Config{}, newValidationError(op, "userAgent", "user agent must not contain control characters")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if err := validateBaseURL(op, cfg.BaseURL); err != nil {
		return Config{}, err
	}
	if cfg.Timeout < 0 {
		return Config{}, newValidationError(op, "timeout", "timeout must not be negative")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
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

func validateBaseURL(op, value string) error {
	if problem := httpx.CheckBaseURL(value); problem != "" {
		return newValidationError(op, "baseURL", problem)
	}
	return nil
}
