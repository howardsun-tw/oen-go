package oen

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNumericAmountBoundaries(t *testing.T) {
	for _, raw := range []string{`"2/2"`, `"0x10"`, `"01"`, `"+1"`, `9223372036854775808`, `-9223372036854775809`, `1e-1000`} {
		t.Run(raw, func(t *testing.T) { _, err := parseAmount(json.RawMessage(raw)); hasError(t, err) })
	}
	for _, tt := range []struct {
		raw  string
		want Amount
	}{{`9007199254740993`, 9007199254740993}, {`9223372036854775807`, 9223372036854775807}, {`-9223372036854775808`, -9223372036854775808}, {`1e3`, 1000}, {`1000.0`, 1000}, {`"1000"`, 1000}} {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseAmount(json.RawMessage(tt.raw))
			noError(t, err)
			equal(t, tt.want, got)
		})
	}
}

func TestNumericOptionalIntTreatsEmptyAsAbsent(t *testing.T) {
	for _, raw := range []string{``, `null`, `""`, `" "`} {
		t.Run(raw, func(t *testing.T) {
			got, err := parseOptionalInt(json.RawMessage(raw))
			noError(t, err)
			equal(t, 0, got)
		})
	}
	for _, raw := range []string{`{}`, `[]`, `"x"`, `"+5"`, `"01"`, `1.5`, `9223372036854775808`} {
		t.Run(raw, func(t *testing.T) {
			_, err := parseOptionalInt(json.RawMessage(raw))
			hasError(t, err)
		})
	}
	// Integers follow the same grammar as amounts, so a value accepted as
	// unitPrice is accepted as quantity too.
	for _, tt := range []struct {
		raw  string
		want int
	}{{`1.0`, 1}, {`1e2`, 100}, {`"12"`, 12}, {`-3`, -3}} {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseOptionalInt(json.RawMessage(tt.raw))
			noError(t, err)
			equal(t, tt.want, got)
		})
	}
}

func TestNumericInvalidIntegersRejectResources(t *testing.T) {
	_, client := newFake(t)
	for _, value := range []string{`9223372036854775808`, `-9223372036854775809`, `1.5`, `"bad"`, `true`, `{}`} {
		for _, field := range []string{"period", "numberOfPeriods"} {
			t.Run(field+value, func(t *testing.T) {
				resource := json.RawMessage(`{"id":"S1","amount":1,"` + field + `":` + value + `}`)
				_, err := decodeSubscription(resource, time.UTC, true)
				hasError(t, err)
				if field == "period" {
					_, err = decodeTransaction(resource, time.UTC)
					hasError(t, err)
				}
				_, err = client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,"` + field + `":` + value + `}`))
				errIs(t, err, ErrInvalidRequest)
			})
		}
		t.Run("quantity"+value, func(t *testing.T) {
			_, err := client.ParseWebhook([]byte(`{"merchantId":"oentech","id":"P1","purpose":"charge","success":true,"productDetails":[{"unitPrice":1,"quantity":` + value + `}]}`))
			errIs(t, err, ErrInvalidRequest)
		})
	}
}
