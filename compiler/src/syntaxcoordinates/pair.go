// Package syntaxcoordinates plans exact source-position updates between matching syntax trees.
package syntaxcoordinates

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Pair visits corresponding positions without mutating either tree. When the
// body changed, the caller will replace it separately and its positions must
// not be applied to the old body.
func Pair(live, fresh *ast.FuncDecl, bodyChanged bool, add func(reflect.Value, lexer.Pos)) error {
	var pair func(reflect.Value, reflect.Value, string) error
	pair = func(old, next reflect.Value, path string) error {
		if old.Type() != next.Type() {
			return fmt.Errorf("coordinate syntax type differs at %s", path)
		}
		if old.Type() == reflect.TypeFor[lexer.Pos]() {
			if !old.CanSet() {
				return fmt.Errorf("coordinate syntax is not writable at %s", path)
			}
			add(old, next.Interface().(lexer.Pos))
			return nil
		}
		switch old.Kind() {
		case reflect.Interface, reflect.Pointer:
			if old.IsNil() != next.IsNil() {
				return fmt.Errorf("coordinate syntax presence differs at %s", path)
			}
			if !old.IsNil() {
				return pair(old.Elem(), next.Elem(), path+".*")
			}
		case reflect.Struct:
			for i := 0; i < old.NumField(); i++ {
				field := old.Type().Field(i).Name
				if old.Type() == reflect.TypeFor[ast.FuncDecl]() && field == "Body" && bodyChanged {
					continue
				}
				if err := pair(old.Field(i), next.Field(i), path+"."+field); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			if old.Len() != next.Len() {
				return fmt.Errorf("coordinate syntax length differs at %s", path)
			}
			for i := 0; i < old.Len(); i++ {
				if err := pair(old.Index(i), next.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		case reflect.Map:
			if old.Len() != 0 || next.Len() != 0 {
				return fmt.Errorf("coordinate syntax map unsupported at %s", path)
			}
		default:
			if !reflect.DeepEqual(old.Interface(), next.Interface()) {
				return fmt.Errorf("coordinate syntax value differs at %s", path)
			}
		}
		return nil
	}
	return pair(reflect.ValueOf(live), reflect.ValueOf(fresh), live.Name)
}
