package sprout

import (
	"context"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/wxy365/basal/errs"
)

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

type Validator interface {
	ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc
}

type ValidateFunc func(ctx context.Context, fieldIdx int, struValue reflect.Value) error

type ObjectValidateFunc func(ctx context.Context, obj reflect.Value) error

type RequiredValidator struct{}

func (r *RequiredValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	frags := strings.Split(validateTag, ";")
	for _, frag := range frags {
		if frag == "required" {
			return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
				fieldValue := struValue.Field(fieldIdx)
				if fieldValue.IsZero() {
					return errs.I18nNew(ctx, "sprout.params.required", struType.Field(fieldIdx).Name, http.StatusBadRequest)
				}
				return nil
			}
		}
	}
	return nil
}

type RequiredByValidator struct{}

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

type EitherValidator struct{}

func (e *EitherValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	frags := strings.Split(validateTag, ";")
	for _, frag := range frags {
		if strings.HasPrefix(frag, "either=") {
			fieldNames := strings.TrimPrefix(frag, "either=")
			if fieldNames == "" {
				return nil
			}
			var validFieldNames []string
			for _, fieldName := range strings.Split(frag, ",") {
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

type NumberRangeValidator struct{}

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

type StringLengthValidator struct{}

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
		fieldLen := len([]rune(fieldValue))
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

type EmailValidator struct{}

func (e *EmailValidator) ValidateFunc(validateTag string, fieldIdx int, struType reflect.Type) ValidateFunc {
	for _, frag := range strings.Split(validateTag, ";") {
		frag = strings.TrimSpace(frag)
		if frag == "email" {
			reg := regexp.MustCompile(`^[\w-]+(\.[\w-]+)*@[\w-]+(\.[\w-]+)+$`)
			return func(ctx context.Context, fieldIdx int, struValue reflect.Value) error {
				fieldValue := struValue.Field(fieldIdx).String()
				if !reg.MatchString(fieldValue) {
					return errs.I18nNew(ctx, "sprout.params.invalid-email", fieldValue).WithStatus(http.StatusBadRequest)
				}
				return nil
			}
		}
	}
	return nil
}
