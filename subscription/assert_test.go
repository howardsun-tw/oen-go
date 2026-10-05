package subscription

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func equal[T any](t *testing.T, want, got T, args ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %#v, got %#v%s", want, got, note(args))
	}
}

func isTrue(t *testing.T, got bool, args ...any) {
	t.Helper()
	if !got {
		t.Fatalf("want true, got false%s", note(args))
	}
}

func isFalse(t *testing.T, got bool, args ...any) {
	t.Helper()
	if got {
		t.Fatalf("want false, got true%s", note(args))
	}
}

func noError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func errIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func errIsNot(t *testing.T, err, target error) {
	t.Helper()
	if errors.Is(err, target) {
		t.Fatalf("error must not match %v: %v", target, err)
	}
}

func contains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("want %q to contain %q", haystack, needle)
	}
}

func notContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("want %q to not contain %q", haystack, needle)
	}
}

func jsonEqual(t *testing.T, want, got string) {
	t.Helper()
	var wantValue, gotValue any
	noError(t, json.Unmarshal([]byte(want), &wantValue))
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("got is not JSON (%v): %s", err, got)
	}
	if !reflect.DeepEqual(wantValue, gotValue) {
		t.Fatalf("want JSON %s, got %s", want, got)
	}
}

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("want a ValidationError, got %v", err)
	}
	return validation.Field
}

func note(args []any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		if s, ok := arg.(string); ok {
			parts[i] = s
			continue
		}
		encoded, _ := json.Marshal(arg)
		parts[i] = string(encoded)
	}
	return " (" + strings.Join(parts, " ") + ")"
}
