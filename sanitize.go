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
		for childKey, childValue := range value {
			// Oen also places card data in a scalar paymentInfo field. Its
			// meaning depends on siblings; LINE Pay references stay intact.
			if normalizeKey(childKey) == "paymentinfo" && cardPaymentContext(value) {
				switch scalar := childValue.(type) {
				case string:
					clean[childKey] = lastFour(scalar)
					continue
				case json.Number:
					clean[childKey] = lastFour(scalar.String())
					continue
				}
			}
			clean[childKey] = sanitizeValue(childKey, childValue)
		}
		return clean
	case []any:
		clean := make([]any, len(value))
		for i, childValue := range value {
			clean[i] = sanitizeValue("", childValue)
		}
		return clean
	default:
		return value
	}
}

func normalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(key)
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

func cardPaymentContext(object map[string]any) bool {
	for key, value := range object {
		text, ok := value.(string)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		switch normalizeKey(key) {
		case "purpose":
			if text == string(PurposeToken) {
				return true
			}
		case "paymentmethod", "method":
			if text == string(MethodCard) {
				return true
			}
		}
	}
	return false
}
