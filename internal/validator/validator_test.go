package validator

import "testing"

func TestValidatorCheck(t *testing.T) {
	validator := New()

	validator.Check(true, "name", "name is required")
	if !validator.Valid() {
		t.Fatal("expected validator to be valid after a passing check")
	}

	validator.Check(false, "name", "name is required")
	if validator.Valid() {
		t.Fatal("expected validator to be invalid after a failing check")
	}

	if got := validator.Errors["name"]; got != "name is required" {
		t.Fatalf("expected name error %q, got %q", "name is required", got)
	}
}