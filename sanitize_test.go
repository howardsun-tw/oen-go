package oen

import (
	"encoding/json"
	"testing"
)

func TestSanitizeJSONRemovesSecretsAndReducesCardNumbers(t *testing.T) {
	const (
		pan   = "4242424242424242"
		token = "2etM3aQSCMWv7OGYQ6gDWtcOJaR"
	)
	raw := []byte(`{
		"token":"2etM3aQSCMWv7OGYQ6gDWtcOJaR",
		"authCode":"831000",
		"amount":1000,
		"paymentInfo":{
			"cardNum":"4242424242424242",
			"cardType":"Visa",
			"cvv":"123",
			"sk":"171291104995271916",
			"account":"98557016831048"
		},
		"nested":[{"providerToken":"2etM3aQSCMWv7OGYQ6gDWtcOJaR","pan":"4242424242424242"}]
	}`)

	clean, err := SanitizeJSON(raw)
	noError(t, err)
	text := string(clean)
	notContains(t, text, pan)
	notContains(t, text, token)
	notContains(t, text, "171291104995271916")
	contains(t, text, `"cardNum":"4242"`)
	contains(t, text, `"pan":"4242"`)
	contains(t, text, "831000")
	// The ATM virtual account is what the payer needs; it is not a secret.
	contains(t, text, "98557016831048")

	var decoded map[string]any
	noError(t, json.Unmarshal(clean, &decoded))
	info, ok := decoded["paymentInfo"].(map[string]any)
	isTrue(t, ok)
	equal(t, "4242", info["cardNum"])
	equal(t, redactedValue, info["cvv"])
	equal(t, redactedValue, info["sk"])
	// Numbers keep their literal form instead of turning into 1e+03.
	contains(t, text, `"amount":1000`)
}

func TestSanitizeJSONRejectsInputItCannotProve(t *testing.T) {
	for _, raw := range []string{``, `   `, `not json`, `{"a":1}{"b":2}`} {
		_, err := SanitizeJSON([]byte(raw))
		hasError(t, err, raw)
	}
}

func TestSanitizeMetadataAppliesTheSameRules(t *testing.T) {
	clean := SanitizeMetadata(map[string]string{
		"token":        "2etM3aQSCMWv7OGYQ6gDWtcOJaR",
		"card_num":     "4242424242424242",
		"securityCode": "123",
		"auth_code":    "831000",
		"order_id":     "ORDER00001",
	})
	equal(t, redactedValue, clean["token"])
	equal(t, "4242", clean["card_num"])
	equal(t, redactedValue, clean["securityCode"])
	equal(t, "831000", clean["auth_code"])
	equal(t, "ORDER00001", clean["order_id"])
	equal(t, map[string]string(nil), SanitizeMetadata(nil))
}

func TestSanitizeCardNumberKeepsOnlyTheLastFour(t *testing.T) {
	equal(t, "4242", SanitizeCardNumber("4242424242424242"))
	equal(t, "4242", SanitizeCardNumber(" 424242******4242 "))
	equal(t, "424", SanitizeCardNumber("424"))
	equal(t, "", SanitizeCardNumber(""))
}

func TestRedactedResourceNeverPanicsOnBadInput(t *testing.T) {
	equal(t, json.RawMessage(`{"redaction":"unavailable"}`), redactedResource(json.RawMessage(`not json`)))
	contains(t, string(redactedResource(json.RawMessage(`{"id":"P1"}`))), "P1")
}

func TestScalarPaymentInfoSanitizationUsesNormalizedContext(t *testing.T) {
	for _, raw := range []string{
		`{"purpose":" token ","paymentInfo":"4242424242424242"}`,
		`{"paymentMethod":"card","paymentInfo":4242424242424242}`,
		`{"data":[{"payment_method":"card","paymentInfo":"4242424242424242"}]}`,
	} {
		clean, err := SanitizeJSON([]byte(raw))
		noError(t, err)
		notContains(t, string(clean), "4242424242424242")
		contains(t, string(clean), `"paymentInfo":"4242"`)
	}
	clean := SanitizeMetadata(map[string]string{"purpose": "token", "paymentInfo": "4242424242424242"})
	equal(t, "4242", clean["paymentInfo"])
}
