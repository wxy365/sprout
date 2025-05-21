package sprout

import "strings"

type Validator interface {
	Accept(pattern string) (bool, string)
	Validate(fieldValue any, object any, patternSlice string) error
}

type RequiredValidator struct {}

func (r *RequiredValidator) Accept(pattern string) (bool, string) {
	if strings.Contains(pattern, "required")
}

func (r *RequiredValidator) Validate(fieldValue any, object any, patternSlice string) error {

}