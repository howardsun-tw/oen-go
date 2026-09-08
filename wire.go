package oen

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// envelope is Oen's response wrapper: {"code":"","message":"","data":{}}.
type envelope struct {
	Code    json.RawMessage `json:"code"`
	Message json.RawMessage `json:"message"`
	Msg     json.RawMessage `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func envCode(e *envelope) string {
	if e == nil {
		return ""
	}
	return rawString(e.Code)
}

func envMessage(e *envelope) string {
	if e == nil {
		return ""
	}
	if message := rawString(e.Message); message != "" {
		return message
	}
	return rawString(e.Msg)
}

// rawString reads a JSON scalar as text. Oen returns numeric identifiers as
// numbers in some responses and as strings in others, so both are accepted.
func rawString(raw json.RawMessage) string {
	if isNullRaw(raw) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String()
	}
	var boolean bool
	if err := json.Unmarshal(raw, &boolean); err == nil {
		return strconv.FormatBool(boolean)
	}
	return ""
}

// parseOptionalInt preserves absent fields (null or an empty string) while
// rejecting malformed or out-of-range values instead of silently replacing
// them with zero. It reads integers with the same grammar as amounts so a
// value that is accepted as unitPrice is also accepted as quantity.
func parseOptionalInt(raw json.RawMessage) (int, error) {
	if isNullRaw(raw) || isEmptyString(raw) {
		return 0, nil
	}
	number, err := parseAmount(raw)
	if err != nil {
		return 0, fmt.Errorf("expected an integer in range: %w", err)
	}
	if int64(int(number)) != int64(number) {
		return 0, fmt.Errorf("expected an integer in range: %d overflows int", number)
	}
	return int(number), nil
}

// parseAmount reads a whole-currency amount. Oen sends amounts as numbers,
// but a JSON number can arrive as "1000" or 1000.0, and a fractional amount
// is not a value this API can represent.
func parseAmount(raw json.RawMessage) (Amount, error) {
	value := rawString(raw)
	if value == "" {
		return 0, fmt.Errorf("amount is missing")
	}
	// Validate JSON decimal syntax before big.Rat, which also accepts fractions
	// and hexadecimal strings. Keep decimal/exponent parsing exact.
	var decimal json.Number
	if json.Unmarshal([]byte(value), &decimal) != nil || decimal.String() == "" {
		return 0, fmt.Errorf("amount must be a decimal number")
	}
	if amount, err := strconv.ParseInt(value, 10, 64); err == nil {
		return Amount(amount), nil
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok || ratio.Denom().Cmp(big.NewInt(1)) != 0 || !ratio.Num().IsInt64() {
		return 0, fmt.Errorf("amount %q is not a whole number", value)
	}
	return Amount(ratio.Num().Int64()), nil
}

func parseOptionalAmount(raw json.RawMessage) (Amount, error) {
	if isNullRaw(raw) {
		return 0, nil
	}
	return parseAmount(raw)
}

// parseTime reads one Oen timestamp. Oen documents every date field as an ISO
// date in UTC+0; loc is used only for the undocumented forms that carry no
// offset, so a caller that has seen local timestamps can say so. An absent
// value (null or an empty string) is the zero time with no error; a value
// that is present but unreadable is an error, never a silent zero.
func parseTime(raw json.RawMessage, loc *time.Location) (time.Time, error) {
	if isNullRaw(raw) || isEmptyString(raw) {
		return time.Time{}, nil
	}
	if isJSONNumber(raw) {
		// A bare epoch is undocumented but unambiguous when it arrives as a
		// JSON number. A digit string is not read as one: "20240412" is far
		// more likely a date than a second in 1970.
		return parseEpoch(rawString(raw))
	}
	value := rawString(raw)
	if value == "" {
		return time.Time{}, fmt.Errorf("timestamp %s is not a string", strings.TrimSpace(string(raw)))
	}
	if when, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return when, nil
	}
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if when, err := time.ParseInLocation(layout, value, loc); err == nil {
			return when, nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp %q is not an ISO date", value)
}

func parseEpoch(value string) (time.Time, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q is not a whole epoch: %w", value, err)
	}
	if number > 100000000000 {
		return time.UnixMilli(number).UTC(), nil
	}
	return time.Unix(number, 0).UTC(), nil
}

// optionalTime reads a timestamp that may be absent, returning nil for it.
func optionalTime(raw json.RawMessage, loc *time.Location) (*time.Time, error) {
	when, err := parseTime(raw, loc)
	if err != nil || when.IsZero() {
		return nil, err
	}
	return &when, nil
}

func isJSONNumber(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && (trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9'))
}

func isEmptyString(raw json.RawMessage) bool {
	var text string
	return json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) == ""
}

func isNullRaw(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || strings.EqualFold(trimmed, "null")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func urlPathSegment(value string) string { return url.PathEscape(value) }
