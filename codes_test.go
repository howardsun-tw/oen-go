package oen

import (
	"net/http"
	"testing"
)

// LookupCode promises the Kind the SDK assigns, so every entry must agree
// with classify for a response at the documented status.
func TestCodeCatalogMatchesClassification(t *testing.T) {
	for code, info := range codeCatalog {
		t.Run(code, func(t *testing.T) {
			err := classify("ChargeToken", http.MethodPost, info.HTTPStatus, nil, envelopeFor(code, "provider said so"), nil, classifyNow)
			hasError(t, err)
			equal(t, info.Kind, err.Kind)
			isTrue(t, info.Description != "" && info.OfficialDescription != "" && info.Action != "")
		})
	}
}

func TestLookupCode(t *testing.T) {
	info, ok := LookupCode(" t0004 ")
	isTrue(t, ok)
	equal(t, CodeInsufficientFunds, info.Code)
	equal(t, http.StatusBadRequest, info.HTTPStatus)
	equal(t, KindDeclined, info.Kind)
	equal(t, "額度不足", info.OfficialDescription)

	// Success is not an error, and card-update page codes come from an
	// endpoint the SDK does not call.
	for _, code := range []string{CodeSuccess, "C0001", "X0001", ""} {
		_, ok := LookupCode(code)
		isFalse(t, ok, code)
	}
}

func TestErrorDescription(t *testing.T) {
	declined := classify("ChargeToken", http.MethodPost, http.StatusBadRequest, nil, envelopeFor("k0001", "RISK"), nil, classifyNow)
	equal(t, "Risk control refused the payment: an individual merchant's monthly total or single amount is over the threshold.", declined.Description())

	undocumented := classify("ChargeToken", http.MethodPost, http.StatusBadRequest, nil, envelopeFor("T9999", ""), nil, classifyNow)
	equal(t, "", undocumented.Description())

	transport := unknownOutcome("ChargeToken", nil)
	equal(t, "", transport.Description())
}
