package retainedsyntax

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"fmt"
	"reflect"
	"strings"
)

// retainedHeader is fn without its statements. Its contracts are part of it (the
// formatter does not print them): callers' proofs read them, so a contract edit is a
// signature edit.
func Header(fn *ast.FuncDecl) string {
	c := *fn
	c.Body = nil
	header := unparse.FormatDecl(&c)
	var b strings.Builder
	// Most contracts are empty, but their labels are always part of the fingerprint.
	b.Grow(len(header) + 224)
	b.WriteString(header)
	writeRetainedContractPart(&b, "\nrequires: ", fn.Requires)
	writeRetainedContractPart(&b, "\nrequires-proofs: ", fn.RequiresProofs)
	writeRetainedContractPart(&b, "\nensure: ", fn.EnsureValues)
	writeRetainedContractPart(&b, "\nensure-proofs: ", fn.EnsureProofs)
	writeRetainedContractPart(&b, "\nensures: ", fn.Ensures)
	writeRetainedContractPart(&b, "\ndecreases: ", fn.Decreases)
	b.WriteString("\ndecreases-wild: ")
	b.WriteString(fn.DecreasesWild)
	writeRetainedContractPart(&b, "\nchanges: ", fn.Changes)
	writeRetainedContractPart(&b, "\npreserves: ", fn.Preserves)
	writeRetainedContractPart(&b, "\nfulfills: ", fn.Fulfills)
	writeRetainedContractPart(&b, "\ncontract-includes: ", fn.ContractIncludes)
	writeRetainedContractPart(&b, "\nuses: ", fn.Uses)
	writeRetainedContractPart(&b, "\nparam-contracts: ", fn.ParamContracts)
	writeRetainedContractPart(&b, "\nforbids: ", fn.Forbids)
	return b.String()
}

func writeRetainedContractPart(b *strings.Builder, label string, value any) {
	b.WriteString(label)
	retainedWriteSyntax(b, reflect.ValueOf(value))
}

// retainedWriteSyntax prints v without source positions: expressions and statements
// through the formatter, other values field by field.
func retainedWriteSyntax(b *strings.Builder, v reflect.Value) {
	if !v.IsValid() {
		b.WriteString("<nil>")
		return
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			b.WriteString("<nil>")
			return
		}
		switch n := v.Interface().(type) {
		case ast.Expr:
			b.WriteString(unparse.FormatExpr(n))
			return
		case ast.Stmt:
			b.WriteString(unparse.FormatStmt(n))
			return
		}
		retainedWriteSyntax(b, v.Elem())
		return
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			retainedWriteSyntax(b, v.Index(i))
			b.WriteString(";")
		}
		b.WriteString("]")
	case reflect.Struct:
		if v.Type() == retainedPosType {
			return
		}
		b.WriteString("{")
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			retainedWriteSyntax(b, v.Field(i))
			b.WriteString(",")
		}
		b.WriteString("}")
	case reflect.Map:
		b.WriteString(fmt.Sprint(v.Len()))
	default:
		fmt.Fprint(b, v.Interface())
	}
}
