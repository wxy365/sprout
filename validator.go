package sprout

import (
	"context"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wxy365/basal/errs"
)

var emailRegex = regexp.MustCompile(`^[\w-]+(\.[\w-]+)*@[\w-]+(\.[\w-]+)+$`)

func defaultValidators() []Validator {
	return []Validator{
		&RequiredValidator{},
		&RequiredByValidator{},
		&EitherValidator{},
		&NumberRangeValidator{},
		&StringLengthValidator{},
		&EmailValidator{},
	}
}

// Validator inspects a struct field's validation tag and, if the tag matches,
// returns a ValidateFunc that performs the actual validation at request time.
type Validator interface {
	// ValidateFunc examines the validateTag for the struct field at fieldIdx
	// within struType and returns a ValidateFunc if the tag is recognized.
	ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc
}

// ValidateFunc validates a single struct field value. It returns an error
// if the field value does not satisfy the validation rule.
type ValidateFunc func(ctx context.Context, fieldIdx int, struValue reflect.Value) error

// ObjectValidateFunc validates an entire struct value. It is typically built
// by composing per-field ValidateFunc instances into a single function.
type ObjectValidateFunc func(ctx context.Context, obj reflect.Value) error

// RequiredValidator validates that a field is not the zero value when its
// validation tag contains "required".
type RequiredValidator struct{}

// ValidateFunc checks for a "required" fragment in the validate tag and returns
// a function that rejects zero-valued fields.
func (r *RequiredValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	frags := strings.Split(validateTag, ";")
	for _, frag := range frags {
		if frag == "required" {
			return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
				fieldValue := struValue.Field(fieldIdx)
				if fieldValue.IsZero() {
					return errs.I18nNew(ctx, "sprout.params.required", struType.Field(fieldIdx).Name).WithStatus(http.StatusBadRequest)
				}
				return nil
			}
		}
	}
	return nil
}

// RequiredByValidator validates that a field is required when at least one
// of the named sibling fields is non-zero. The tag format is "required_by=FieldA,FieldB".
type RequiredByValidator struct{}

// ValidateFunc checks for a "required_by=" fragment in the validate tag and returns
// a function that enforces conditional required validation.
func (r *RequiredByValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	frags := strings.Split(validateTag, ";")
	for _, frag := range frags {
		if strings.HasPrefix(frag, "required_by=") {
			fieldNames := strings.TrimPrefix(frag, "required_by=")
			if fieldNames == "" {
				return nil
			}
			var validFieldNames []string
			for _, fieldName := range strings.Split(fieldNames, ",") {
				_, ok := struType.FieldByName(fieldName)
				if ok {
					validFieldNames = append(validFieldNames, fieldName)
				}
			}
			if len(validFieldNames) == 0 {
				return nil
			}
			return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
				fieldValue := struValue.Field(fieldIdx)
				if !fieldValue.IsZero() {
					return nil
				}
				for _, fieldName := range validFieldNames {
					fv := struValue.FieldByName(fieldName)
					if !fv.IsZero() {
						return errs.I18nNew(ctx, "sprout.params.required", struType.Field(fieldIdx).Name).WithStatus(http.StatusBadRequest)
					}
				}
				return nil
			}
		}
	}
	return nil
}

// EitherValidator validates that, among a set of fields, at least one is
// non-zero. The tag format is "either=FieldA,FieldB".
type EitherValidator struct{}

// ValidateFunc checks for an "either=" fragment in the validate tag and returns
// a function that ensures at least one of the named fields has a non-zero value.
func (e *EitherValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	frags := strings.Split(validateTag, ";")
	for _, frag := range frags {
		if strings.HasPrefix(frag, "either=") {
			fieldNames := strings.TrimPrefix(frag, "either=")
			if fieldNames == "" {
				return nil
			}
			var validFieldNames []string
			for _, fieldName := range strings.Split(fieldNames, ",") {
				_, ok := struType.FieldByName(fieldName)
				if ok {
					validFieldNames = append(validFieldNames, fieldName)
				}
			}
			if len(validFieldNames) == 0 {
				return nil
			}
			return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
				fieldValue := struValue.Field(fieldIdx)
				if !fieldValue.IsZero() {
					return nil
				}
				for _, fieldName := range validFieldNames {
					fv := struValue.FieldByName(fieldName)
					if !fv.IsZero() {
						return nil
					}
				}
				return errs.I18nNew(ctx, "sprout.params.require-one", struType.Field(fieldIdx).Name).WithStatus(http.StatusBadRequest)
			}
		}
	}
	return nil
}

// NumberRangeValidator validates that a numeric field falls within a specified
// range. The tag format uses interval notation: "[min,max]", "(min,max]",
// "[min,max)", or "(min,max)".
type NumberRangeValidator struct{}

// ValidateFunc checks for an interval notation fragment in the validate tag and
// returns a function that validates the numeric field is within the specified bounds.
func (n *NumberRangeValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	var bottom, upper string
	var ge, le bool
	for _, frag := range strings.Split(validateTag, ";") {
		frag = strings.TrimSpace(frag)
		if (strings.HasPrefix(frag, "[") || strings.HasPrefix(frag, "(")) && (strings.HasSuffix(frag, "]") || strings.HasSuffix(frag, ")")) {
			ranges := strings.Split(frag[1:len(frag)-1], ",")
			if len(ranges) != 2 {
				return nil
			}
			bottom = strings.TrimSpace(ranges[0])
			upper = strings.TrimSpace(ranges[1])
			if strings.HasPrefix(frag, "[") {
				ge = true
			}
			if strings.HasSuffix(frag, "]") {
				le = true
			}
			break
		}
	}
	if bottom == "" && upper == "" {
		return nil
	}
	field := struType.Field(fieldIdx)
	switch field.Type.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var minVal, maxVal int
		if bottom == "" {
			minVal = math.MinInt
		} else {
			minI64, err := strconv.ParseInt(bottom, 10, 64)
			if err != nil {
				panic(errs.New("The min value [{0}] of {1}.{2} is invalid", bottom, struType.Name(), field.Name))
			}
			minVal = int(minI64)
		}
		if upper == "" {
			maxVal = math.MaxInt
		} else {
			maxI64, err := strconv.ParseInt(upper, 10, 64)
			if err != nil {
				panic(errs.New("The max value [{0}] of {1}.{2} is invalid", upper, struType.Name(), field.Name))
			}
			maxVal = int(maxI64)
		}
		if minVal > maxVal {
			panic(errs.New("The min value [{0}] of {1}.{2} is greater than the max value [{3}]", bottom, struType.Name(), field.Name, upper))
		}
		return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
			fieldValue := struValue.Field(fieldIdx).Int()
			if fieldValue == 0 {
				return nil
			}
			if (ge && fieldValue < int64(minVal)) || (!ge && fieldValue <= int64(minVal)) || (le && fieldValue > int64(maxVal)) || (!le && fieldValue >= int64(maxVal)) {
				return errs.I18nNew(ctx, "sprout.params.out-of-range", struValue.Type().Field(fieldIdx).Name, minVal, maxVal).WithStatus(http.StatusBadRequest)
			}
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var minVal, maxVal uint
		if bottom == "" {
			minVal = 0
		} else {
			minU64, err := strconv.ParseUint(bottom, 10, 64)
			if err != nil {
				panic(errs.New("The min value [{0}] of {1}.{2} is invalid", bottom, struType.Name(), field.Name))
			}
			minVal = uint(minU64)
		}
		if upper == "" {
			maxVal = math.MaxUint
		} else {
			maxU64, err := strconv.ParseUint(upper, 10, 64)
			if err != nil {
				panic(errs.New("The max value [{0}] of {1}.{2} is invalid", upper, struType.Name(), field.Name))
			}
			maxVal = uint(maxU64)
		}
		if minVal > maxVal {
			panic(errs.New("The min value [{0}] of {1}.{2} is greater than the max value [{3}]", bottom, struType.Name(), field.Name, upper))
		}
		return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
			fieldValue := struValue.Field(fieldIdx).Uint()
			if fieldValue == 0 {
				return nil
			}
			if (ge && fieldValue < uint64(minVal)) || (!ge && fieldValue <= uint64(minVal)) || (le && fieldValue > uint64(maxVal)) || (!le && fieldValue >= uint64(maxVal)) {
				return errs.I18nNew(ctx, "sprout.params.out-of-range", struValue.Type().Field(fieldIdx).Name, minVal, maxVal).WithStatus(http.StatusBadRequest)
			}
			return nil
		}
	case reflect.Float32, reflect.Float64:
		var minVal, maxVal float64
		if bottom == "" {
			minVal = math.MinInt64
		} else {
			minF64, err := strconv.ParseFloat(bottom, 64)
			if err != nil {
				panic(errs.New("The min value [{0}] of {1}.{2} is invalid", bottom, struType.Name(), field.Name))
			}
			minVal = minF64
		}
		if upper == "" {
			maxVal = math.MaxFloat64
		} else {
			maxF64, err := strconv.ParseFloat(upper, 64)
			if err != nil {
				panic(errs.New("The max value [{0}] of {1}.{2} is invalid", upper, struType.Name(), field.Name))
			}
			maxVal = maxF64
		}
		if minVal > maxVal {
			panic(errs.New("The min value [{0}] of {1}.{2} is greater than the max value [{3}]", bottom, struType.Name(), field.Name, upper))
		}
		return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
			fieldValue := struValue.Field(fieldIdx).Float()
			if fieldValue == 0 {
				return nil
			}
			if (fieldValue < minVal) || (!ge && fieldValue == minVal) || (fieldValue > maxVal) || (!le && fieldValue == maxVal) {
				return errs.I18nNew(ctx, "sprout.params.out-of-range", struValue.Type().Field(fieldIdx).Name, minVal, maxVal).WithStatus(http.StatusBadRequest)
			}
			return nil
		}
	default:
		return nil
	}
}

// StringLengthValidator validates that a string field's length falls within
// a specified range. The tag format uses interval notation: "[min,max]",
// "(min,max]", "[min,max)", or "(min,max)".
type StringLengthValidator struct{}

// ValidateFunc checks for an interval notation fragment in the validate tag and
// returns a function that validates the string length is within the specified bounds.
func (s *StringLengthValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	var bottom, upper string
	var ge, le bool
	for _, frag := range strings.Split(validateTag, ";") {
		frag = strings.TrimSpace(frag)
		if (strings.HasPrefix(frag, "[") || strings.HasPrefix(frag, "(")) && (strings.HasSuffix(frag, "]") || strings.HasSuffix(frag, ")")) {
			ranges := strings.Split(frag[1:len(frag)-1], ",")
			if len(ranges) != 2 {
				return nil
			}
			bottom = strings.TrimSpace(ranges[0])
			upper = strings.TrimSpace(ranges[1])
			if strings.HasPrefix(frag, "[") {
				ge = true
			}
			if strings.HasSuffix(frag, "]") {
				le = true
			}
			break
		}
	}
	if bottom == "" && upper == "" {
		return nil
	}
	field := struType.Field(fieldIdx)
	if field.Type.Kind() != reflect.String {
		return nil
	}
	var minLen, maxLen int
	var err error
	if bottom != "" {
		minLen, err = strconv.Atoi(bottom)
		if err != nil {
			panic(errs.New("The min length [{0}] of {1}.{2} is invalid", bottom, struType.Name(), field.Name))
		}
	}
	if upper != "" {
		maxLen, err = strconv.Atoi(upper)
		if err != nil {
			panic(errs.New("The max length [{0}] of {1}.{2} is invalid", upper, struType.Name(), field.Name))
		}
	}
	return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
		fieldValue := struValue.Field(fieldIdx).String()
		fieldLen := utf8.RuneCountInString(fieldValue)
		if minLen > 0 {
			if (fieldLen < minLen) || (!ge && fieldLen == minLen) {
				return errs.I18nNew(ctx, "sprout.params.out-of-length", field.Name, minLen, maxLen).WithStatus(http.StatusBadRequest)
			}
		}
		if maxLen > 0 {
			if (fieldLen > maxLen) || (!le && fieldLen == maxLen) {
				return errs.I18nNew(ctx, "sprout.params.out-of-length", field.Name, minLen, maxLen).WithStatus(http.StatusBadRequest)
			}
		}
		return nil
	}
}

// EmailValidator validates that a string field is a valid email address when
// the validation tag contains "email".
type EmailValidator struct{}

// ValidateFunc checks for an "email" fragment in the validate tag and returns
// a function that validates the field value matches the email regex pattern.
func (e *EmailValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	for _, frag := range strings.Split(validateTag, ";") {
		frag = strings.TrimSpace(frag)
		if frag == "email" {
			return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
				fieldValue := struValue.Field(fieldIdx).String()
				if !emailRegex.MatchString(fieldValue) {
					return errs.I18nNew(ctx, "sprout.params.invalid-email", fieldValue).WithStatus(http.StatusBadRequest)
				}
				return nil
			}
		}
	}
	return nil
}
