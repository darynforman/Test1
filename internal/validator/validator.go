package validator

import "strings"

// Validator records validation errors keyed by JSON field name.
type Validator struct {
	Errors map[string]string
}

func New() *Validator {
	return &Validator{Errors: make(map[string]string)}
}

func (v *Validator) Check(ok bool, key, message string) {
	// A failed condition is recorded under the related field name.
	if !ok {
		v.AddError(key, message)
	}
}

func (v *Validator) AddError(key, message string) {
	// Keep the first message so later checks do not hide the original problem.
	if _, exists := v.Errors[key]; !exists {
		v.Errors[key] = message
	}
}

func (v *Validator) Valid() bool {
	return len(v.Errors) == 0
}

func Matches(value string, rx interface{ MatchString(string) bool }) bool {
	return rx.MatchString(value)
}

func NotBlank(value string) bool {
	return strings.TrimSpace(value) != ""
}
