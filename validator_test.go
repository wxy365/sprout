package sprout

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

// validatorSample is the input entity used by the validator tests. It exercises
// every validation rule supported by the built-in validators.
type validatorSample struct {
	Name     string  `validate:"required;[3,32]"`
	Age      int     `validate:"[18,60]"`
	Score    float64 `validate:"(0,100]"`
	Email    string  `validate:"email"`
	Alias    string  `validate:"required_by=Name"`
	Nickname string  `validate:"either=Name,Email"`
}

// validateField runs the given validator against the named field of the sample
// struct and returns the validation error, if any.
func validateField(t *testing.T, v Validator, tag, fieldName string, obj any) error {
	t.Helper()
	rv := reflect.ValueOf(obj)
	rt := rv.Type()
	idx := -1
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Name == fieldName {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("the field %q does not exist in %s", fieldName, rt.Name())
	}
	fn := v.ValidateFunc(tag, idx, rt)
	if fn == nil {
		return nil
	}
	return fn(context.Background(), idx, rv)
}

func TestRequiredValidator(t *testing.T) {
	v := &RequiredValidator{}
	type sample struct {
		Name string `validate:"required"`
	}

	if fn := v.ValidateFunc("required", 0, reflect.TypeOf(sample{})); fn == nil {
		t.Fatal("the required validator should recognize the 'required' tag")
	}
	if fn := v.ValidateFunc("email", 0, reflect.TypeOf(sample{})); fn != nil {
		t.Fatal("the required validator should ignore the 'email' tag")
	}

	// a zero value is rejected
	if err := validateField(t, v, "required", "Name", sample{}); err == nil {
		t.Error("an empty required field should be rejected")
	} else if got := err.Error(); got == "" {
		t.Error("the validation error should carry a message")
	}

	// a non-zero value is accepted
	if err := validateField(t, v, "required", "Name", sample{Name: "Foo"}); err != nil {
		t.Errorf("a filled required field should be accepted, but got: %v", err)
	}
}

func TestRequiredByValidator(t *testing.T) {
	v := &RequiredByValidator{}
	type sample struct {
		Name  string `validate:"required_by=Other"`
		Other string `validate:"required_by=Name"`
	}

	// the field is not required while none of the referenced fields is set
	if err := validateField(t, v, "required_by=Other", "Name", sample{}); err != nil {
		t.Errorf("the field should not be required when the referenced field is empty, but got: %v", err)
	}

	// the field becomes required as soon as the referenced field is set
	err := validateField(t, v, "required_by=Other", "Name", sample{Other: "x"})
	if err == nil {
		t.Error("the field should be required when the referenced field is set")
	}

	// an unknown referenced field is ignored
	if fn := v.ValidateFunc("required_by=NoSuchField", 0, reflect.TypeOf(sample{})); fn != nil {
		t.Error("the validator should ignore an unknown referenced field")
	}
}

func TestEitherValidator(t *testing.T) {
	v := &EitherValidator{}
	type sample struct {
		Name  string `validate:"either=Alias,Email"`
		Alias string
		Email string
	}

	// none of the alternatives is set -> rejected
	if err := validateField(t, v, "either=Alias,Email", "Name", sample{}); err == nil {
		t.Error("the field should be rejected when no alternative is set")
	}

	// one alternative is set -> accepted
	if err := validateField(t, v, "either=Alias,Email", "Name", sample{Alias: "a"}); err != nil {
		t.Errorf("the field should be accepted when an alternative is set, but got: %v", err)
	}
	if err := validateField(t, v, "either=Alias,Email", "Name", sample{Email: "a@b.com"}); err != nil {
		t.Errorf("the field should be accepted when an alternative is set, but got: %v", err)
	}
}

func TestNumberRangeValidator(t *testing.T) {
	v := &NumberRangeValidator{}
	type sample struct {
		Age      int     `validate:"[18,60]"`
		Ratio    float64 `validate:"(0,100]"`
		Count    uint    `validate:"[1,10]"`
		Untagged string
	}

	cases := []struct {
		name    string
		field   string
		tag     string
		obj     sample
		wantErr bool
	}{
		{"int inside the closed range", "Age", "[18,60]", sample{Age: 30}, false},
		{"int on the lower bound", "Age", "[18,60]", sample{Age: 18}, false},
		{"int on the upper bound", "Age", "[18,60]", sample{Age: 60}, false},
		{"int below the lower bound", "Age", "[18,60]", sample{Age: 17}, true},
		{"int above the upper bound", "Age", "[18,60]", sample{Age: 61}, true},
		{"float inside the half-open range", "Ratio", "(0,100]", sample{Ratio: 50}, false},
		{"float on the excluded lower bound", "Ratio", "(0,100]", sample{Ratio: 0}, false},
		{"float above the upper bound", "Ratio", "(0,100]", sample{Ratio: 100.5}, true},
		{"uint inside the closed range", "Count", "[1,10]", sample{Count: 5}, false},
		{"uint below the lower bound", "Count", "[1,10]", sample{Count: 0}, false},
		{"uint above the upper bound", "Count", "[1,10]", sample{Count: 11}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateField(t, v, tc.tag, tc.field, tc.obj)
			if tc.wantErr && err == nil {
				t.Errorf("the value should be rejected")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("the value should be accepted, but got: %v", err)
			}
		})
	}

	// a tag that is not an interval is ignored
	if fn := v.ValidateFunc("required", 0, reflect.TypeOf(sample{})); fn != nil {
		t.Error("the number range validator should ignore a non-interval tag")
	}
	// a non-numeric field is ignored
	if fn := v.ValidateFunc("[1,10]", 3, reflect.TypeOf(sample{})); fn != nil {
		t.Error("the number range validator should ignore a non-numeric field")
	}
}

func TestStringLengthValidator(t *testing.T) {
	v := &StringLengthValidator{}
	type sample struct {
		Name  string `validate:"[3,32]"`
		Short string `validate:"[1,2]"`
	}

	cases := []struct {
		name    string
		field   string
		tag     string
		value   string
		wantErr bool
	}{
		{"inside the range", "Name", "[3,32]", "abcd", false},
		{"on the lower bound", "Name", "[3,32]", "abc", false},
		{"too short", "Name", "[3,32]", "ab", true},
		{"on the upper bound", "Name", "[3,32]", "abcdefghijklmnopqrstuvwxyz012345", false},
		{"too long", "Name", "[3,32]", "abcdefghijklmnopqrstuvwxyz0123456", true},
		{"the length is counted in runes", "Name", "[3,32]", "中文名", false},
		{"the length is counted in runes, not bytes", "Name", "[3,32]", "中", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateField(t, v, tc.tag, tc.field, sample{Name: tc.value, Short: tc.value})
			if tc.wantErr && err == nil {
				t.Errorf("the value %q should be rejected", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("the value %q should be accepted, but got: %v", tc.value, err)
			}
		})
	}

	// a non-string field is ignored
	if fn := v.ValidateFunc("[3,32]", 0, reflect.TypeOf(struct {
		Age int
	}{})); fn != nil {
		t.Error("the string length validator should ignore a non-string field")
	}
}

func TestEmailValidator(t *testing.T) {
	v := &EmailValidator{}
	type sample struct {
		Email string `validate:"email"`
	}

	cases := []struct {
		email   string
		wantErr bool
	}{
		{"foo@example.com", false},
		{"foo.bar@sub.example.com", false},
		{"foo-bar@example.co.uk", false},
		{"", true},
		{"foo", true},
		{"foo@", true},
		{"@example.com", true},
		{"foo@example", true},
	}

	for _, tc := range cases {
		t.Run(tc.email, func(t *testing.T) {
			err := validateField(t, v, "email", "Email", sample{Email: tc.email})
			if tc.wantErr && err == nil {
				t.Errorf("the email %q should be rejected", tc.email)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("the email %q should be accepted, but got: %v", tc.email, err)
			}
		})
	}
}

// TestBuildInputEntityValidateFuncs verifies that the validators are composed
// per field and that the composed function rejects the whole object when any
// of its fields is invalid.
func TestBuildInputEntityValidateFuncs(t *testing.T) {
	svr := newDefaultServer("test")
	validateFunc := svr.buildInputEntityValidateFuncs(reflect.TypeOf(validatorSample{}))
	if validateFunc == nil {
		t.Fatal("the validate function should not be nil")
	}

	// a valid object passes
	valid := validatorSample{Name: "Foo", Age: 30, Score: 50, Email: "foo@example.com", Alias: "bar"}
	if err := validateFunc(context.Background(), reflect.ValueOf(valid)); err != nil {
		t.Errorf("the valid object should be accepted, but got: %v", err)
	}

	// the name is too short
	invalid := valid
	invalid.Name = "ab"
	err := validateFunc(context.Background(), reflect.ValueOf(invalid))
	if err == nil {
		t.Error("the object with a too short name should be rejected")
	} else if status := errStatus(err); status != http.StatusBadRequest {
		t.Errorf("the status code = %d, want %d", status, http.StatusBadRequest)
	}

	// the age is out of range
	invalid = valid
	invalid.Age = 100
	if err := validateFunc(context.Background(), reflect.ValueOf(invalid)); err == nil {
		t.Error("the object with an out-of-range age should be rejected")
	}

	// the email is invalid
	invalid = valid
	invalid.Email = "not-an-email"
	if err := validateFunc(context.Background(), reflect.ValueOf(invalid)); err == nil {
		t.Error("the object with an invalid email should be rejected")
	}
}

// TestBuildInputEntityValidateFuncsNested verifies that the validators of the
// nested structs are composed into the parent one.
func TestBuildInputEntityValidateFuncsNested(t *testing.T) {
	type nested struct {
		Code string `validate:"required"`
	}
	type outer struct {
		ID     string `validate:"required"`
		Nested nested
	}

	svr := newDefaultServer("test")
	validateFunc := svr.buildInputEntityValidateFuncs(reflect.TypeOf(outer{}))
	if validateFunc == nil {
		t.Fatal("the validate function should not be nil")
	}

	if err := validateFunc(context.Background(), reflect.ValueOf(outer{ID: "1", Nested: nested{Code: "c"}})); err != nil {
		t.Errorf("the valid object should be accepted, but got: %v", err)
	}
	if err := validateFunc(context.Background(), reflect.ValueOf(outer{ID: "1"})); err == nil {
		t.Error("the object with an empty nested field should be rejected")
	}
}
