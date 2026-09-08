package oen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const redactedValue = "REDACTED"

// SanitizeJSON returns a canonical copy of a provider payload with secrets
// replaced by "REDACTED" and card numbers reduced to their last four digits.
// Every payload the SDK returns has already been through it.
func SanitizeJSON(raw []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("oen: cannot sanitize empty JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("oen: decode JSON for sanitization: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("oen: JSON carries more than one value")
		}
		return nil, fmt.Errorf("oen: decode trailing JSON for sanitization: %w", err)
	}
	return json.Marshal(sanitizeValue("", value))
}

// SanitizeMetadata copies a string map under the same rules as [SanitizeJSON].
func SanitizeMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	context := make(map[string]any, len(metadata))
	for key, value := range metadata {
		context[key] = value
	}
	sanitized := sanitizeValue("", context).(map[string]any)
	clean := make(map[string]string, len(metadata))
	for key, value := range sanitized {
		clean[key] = value.(string)
	}
	return clean
}

// SanitizeCardNumber keeps only the digits that may be displayed.
func SanitizeCardNumber(cardNumber string) string { return lastFour(cardNumber) }

// redactedResource sanitizes a resource that has already been decoded, so a
// caller always gets a payload it is safe to store even if sanitization of
// some exotic value fails.
func redactedResource(resource json.RawMessage) json.RawMessage {
	clean, err := SanitizeJSON(resource)
	if err != nil {
		return json.RawMessage(`{"redaction":"unavailable"}`)
	}
	return clean
}

func sanitizeValue(key string, value any) any {
	return sanitizeNode(key, value, false)
}

// sanitizeNode walks one value. maskPANs is set inside a paymentInfo subtree
// whose payment method is card or unknown: there, any scalar that looks like
// a card number is reduced to its last four digits even when its key is not
// one of the documented card keys.
func sanitizeNode(key string, value any, maskPANs bool) any {
	switch {
	case isSecretKey(key):
		return redactedValue
	case isCardNumberKey(key):
		if value == nil {
			return ""
		}
		return lastFour(fmt.Sprint(value))
	}

	switch value := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(value))
		context := paymentContextOf(value)
		for childKey, childValue := range value {
			childMask := maskPANs
			// Oen also places card data in a scalar paymentInfo field. Its
			// meaning depends on siblings: a card payment or token binding
			// always carries the card number, LINE Pay carries its own
			// reference, and a payload that says nothing is masked whenever
			// the value looks like a card number.
			if normalizeKey(childKey) == "paymentinfo" && context != paymentContextOther {
				if masked, ok := maskPaymentInfoScalar(childValue, context); ok {
					clean[childKey] = masked
					continue
				}
				childMask = true
			}
			clean[childKey] = sanitizeNode(childKey, childValue, childMask)
		}
		return clean
	case []any:
		clean := make([]any, len(value))
		for i, childValue := range value {
			clean[i] = sanitizeNode("", childValue, maskPANs)
		}
		return clean
	case string:
		if maskPANs && looksLikeCardNumber(value) {
			return lastFour(value)
		}
		return value
	case json.Number:
		if maskPANs && looksLikeCardNumber(value.String()) {
			return lastFour(value.String())
		}
		return value
	default:
		return value
	}
}

// paymentContext is what a payload says about its own payment method.
type paymentContext int

const (
	// paymentContextUnknown means the payload names no payment method.
	paymentContextUnknown paymentContext = iota
	// paymentContextCard is a card payment or a token binding.
	paymentContextCard
	// paymentContextOther is a non-card method such as LINE Pay, whose
	// paymentInfo is a reference rather than a card number.
	paymentContextOther
)

func paymentContextOf(object map[string]any) paymentContext {
	context := paymentContextUnknown
	for key, value := range object {
		text, ok := value.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		switch normalizeKey(key) {
		case "purpose":
			if strings.EqualFold(text, string(PurposeToken)) {
				return paymentContextCard
			}
		case "paymentmethod", "method":
			if strings.EqualFold(text, string(MethodCard)) {
				return paymentContextCard
			}
			if text != "" {
				context = paymentContextOther
			}
		}
	}
	return context
}

// maskPaymentInfoScalar reduces a scalar paymentInfo to its last four
// characters. A card context always masks; an unknown context masks only a
// value that looks like a card number, so a bare reference is preserved.
func maskPaymentInfoScalar(value any, context paymentContext) (any, bool) {
	var text string
	switch scalar := value.(type) {
	case string:
		text = scalar
	case json.Number:
		text = scalar.String()
	default:
		return nil, false
	}
	if context == paymentContextCard || looksLikeCardNumber(text) {
		return lastFour(text), true
	}
	return value, true
}

// looksLikeCardNumber reports a 13 to 19 digit string that passes the Luhn
// check, which every card number does and which provider references such
// as LINE Pay transaction ids almost never do.
func looksLikeCardNumber(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 13 || len(value) > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(value) - 1; i >= 0; i-- {
		digit := int(value[i] - '0')
		if digit < 0 || digit > 9 {
			return false
		}
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}

var keySeparators = strings.NewReplacer("_", "", "-", "", " ", "")

func normalizeKey(key string) string {
	return keySeparators.Replace(strings.ToLower(strings.TrimSpace(key)))
}

// isSecretKey covers the reusable payment token, card security codes and the
// "sk" field Oen puts in ATM payment info.
func isSecretKey(key string) bool {
	switch normalizeKey(key) {
	case "token", "providertoken", "paymenttoken", "accesstoken", "authtoken", "bearer",
		"cvv", "cvc", "securitycode", "securityvalue", "sk":
		return true
	default:
		return false
	}
}

func isCardNumberKey(key string) bool {
	switch normalizeKey(key) {
	case "cardnum", "cardnumber", "pan", "primaryaccountnumber":
		return true
	default:
		return false
	}
}

func lastFour(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 4 {
		return value
	}
	return string(runes[len(runes)-4:])
}
