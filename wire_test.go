package oen

import (
	"encoding/json"
	"testing"
	"time"
)

func rawJSON(value string) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func TestRawStringReadsEveryScalarOenSends(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: `"P20240412QJMAZNML"`, want: "P20240412QJMAZNML"},
		{raw: `"  spaced  "`, want: "spaced"},
		{raw: `1000`, want: "1000"},
		{raw: `1000.5`, want: "1000.5"},
		{raw: `true`, want: "true"},
		{raw: `false`, want: "false"},
		{raw: `null`, want: ""},
		{raw: `{"id":"x"}`, want: ""},
		{raw: ``, want: ""},
	}
	for _, test := range tests {
		equal(t, test.want, rawString(json.RawMessage(test.raw)), test.raw)
	}
}

func TestRawBoolOnlyAcceptsValuesItCanProve(t *testing.T) {
	tests := []struct {
		raw   string
		value bool
		ok    bool
	}{
		{raw: `true`, value: true, ok: true},
		{raw: `false`, ok: true},
		{raw: `"true"`, value: true, ok: true},
		{raw: `"1"`, value: true, ok: true},
		{raw: `"failed"`, ok: true},
		{raw: `"indeterminate"`},
		{raw: `null`},
		{raw: ``},
	}
	for _, test := range tests {
		value, ok := rawBool(json.RawMessage(test.raw))
		equal(t, test.ok, ok, test.raw)
		equal(t, test.value, value, test.raw)
	}
}

func TestParseAmountRejectsFractions(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want Amount
	}{
		{raw: `150`, want: 150},
		{raw: `"150"`, want: 150},
		{raw: `150.0`, want: 150},
		{raw: `0`, want: 0},
		{raw: `-150`, want: -150},
	} {
		value, err := parseAmount(json.RawMessage(test.raw))
		noError(t, err)
		equal(t, test.want, value, test.raw)
	}

	for _, raw := range []string{`150.5`, `"abc"`, `null`, ``} {
		_, err := parseAmount(json.RawMessage(raw))
		hasError(t, err, raw)
	}
}

// Oen documents every timestamp as an ISO date in UTC+0. A value without an
// offset is undocumented, so the location is a caller decision rather than a
// guess baked into the SDK.
func TestParseTimeFollowsTheDocumentedFormats(t *testing.T) {
	when, ok := parseTime(json.RawMessage(`"2024-04-12T07:40:25.502Z"`), time.UTC)
	isTrue(t, ok)
	equal(t, time.Date(2024, 4, 12, 7, 40, 25, 502000000, time.UTC), when.UTC())

	when, ok = parseTime(json.RawMessage(`"2026-09-04T12:34:56+08:00"`), time.UTC)
	isTrue(t, ok)
	equal(t, time.Date(2026, 9, 4, 4, 34, 56, 0, time.UTC), when.UTC())

	taipei := time.FixedZone("UTC+8", 8*60*60)
	for _, raw := range []string{`"2026-09-04 12:34:56"`, `"2026-09-04T12:34:56"`} {
		when, ok = parseTime(json.RawMessage(raw), time.UTC)
		isTrue(t, ok, raw)
		equal(t, time.Date(2026, 9, 4, 12, 34, 56, 0, time.UTC), when.UTC(), raw)

		when, ok = parseTime(json.RawMessage(raw), taipei)
		isTrue(t, ok, raw)
		equal(t, time.Date(2026, 9, 4, 4, 34, 56, 0, time.UTC), when.UTC(), raw)
	}

	when, ok = parseTime(json.RawMessage(`1757332496`), time.UTC)
	isTrue(t, ok)
	equal(t, time.Unix(1757332496, 0).UTC(), when.UTC())

	when, ok = parseTime(json.RawMessage(`1757332496789`), time.UTC)
	isTrue(t, ok)
	equal(t, time.UnixMilli(1757332496789).UTC(), when.UTC())

	for _, raw := range []string{`"not a time"`, `null`, ``, `"2026-13-45"`} {
		_, ok = parseTime(json.RawMessage(raw), time.UTC)
		isFalse(t, ok, raw)
	}
}

func TestEnvelopeReadsEitherMessageField(t *testing.T) {
	equal(t, "", envCode(nil))
	equal(t, "", envMessage(nil))

	env := &envelope{Code: rawJSON("S0000"), Message: rawJSON("done")}
	equal(t, "S0000", envCode(env))
	equal(t, "done", envMessage(env))

	// Some Oen responses use "msg" instead of "message".
	env = &envelope{Msg: rawJSON("legacy field")}
	equal(t, "legacy field", envMessage(env))
}

func TestURLPathSegmentEscapesProviderIdentifiers(t *testing.T) {
	equal(t, "P20240412QJMAZNML", urlPathSegment("P20240412QJMAZNML"))
	equal(t, "a%2Fb%3Fc", urlPathSegment("a/b?c"))
}

func TestTruncateRunesCutsOnRuneBoundaries(t *testing.T) {
	equal(t, "你e", truncateRunes("你e🙂", 2))
	equal(t, "abc", truncateRunes("abc", 10))
}
