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
	when, err := parseTime(json.RawMessage(`"2024-04-12T07:40:25.502Z"`), time.UTC)
	noError(t, err)
	equal(t, time.Date(2024, 4, 12, 7, 40, 25, 502000000, time.UTC), when.UTC())

	when, err = parseTime(json.RawMessage(`"2026-09-04T12:34:56+08:00"`), time.UTC)
	noError(t, err)
	equal(t, time.Date(2026, 9, 4, 4, 34, 56, 0, time.UTC), when.UTC())

	taipei := time.FixedZone("UTC+8", 8*60*60)
	for _, raw := range []string{`"2026-09-04 12:34:56"`, `"2026-09-04T12:34:56"`} {
		when, err = parseTime(json.RawMessage(raw), time.UTC)
		noError(t, err)
		equal(t, time.Date(2026, 9, 4, 12, 34, 56, 0, time.UTC), when.UTC(), raw)

		when, err = parseTime(json.RawMessage(raw), taipei)
		noError(t, err)
		equal(t, time.Date(2026, 9, 4, 4, 34, 56, 0, time.UTC), when.UTC(), raw)
	}

	// A bare epoch is read only when it arrives as a JSON number.
	when, err = parseTime(json.RawMessage(`1757332496`), time.UTC)
	noError(t, err)
	equal(t, time.Unix(1757332496, 0).UTC(), when.UTC())

	when, err = parseTime(json.RawMessage(`1757332496789`), time.UTC)
	noError(t, err)
	equal(t, time.UnixMilli(1757332496789).UTC(), when.UTC())

	// Absent values are the zero time without an error.
	for _, raw := range []string{`null`, ``, `""`, `" "`} {
		when, err = parseTime(json.RawMessage(raw), time.UTC)
		noError(t, err)
		isTrue(t, when.IsZero(), raw)
		optional, err := optionalTime(json.RawMessage(raw), time.UTC)
		noError(t, err)
		isTrue(t, optional == nil, raw)
	}

	// A value that is present but unreadable is an error, never a silent
	// zero: "20240412" is a date, not a second in 1970.
	for _, raw := range []string{`"not a time"`, `"2026-13-45"`, `"20240412"`, `"2024-04-12"`, `1757332496.5`, `true`, `{}`} {
		_, err = parseTime(json.RawMessage(raw), time.UTC)
		hasError(t, err, raw)
		_, err = optionalTime(json.RawMessage(raw), time.UTC)
		hasError(t, err, raw)
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
