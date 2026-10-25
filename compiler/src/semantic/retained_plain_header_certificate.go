package semantic

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Text and typed position contributions are separate authority. Rebinding never
// searches rendered diagnostics for numbers or discards a position from a hash.
type plainHeaderCertificatePart struct {
	Text        string
	SourceField string
	Position    lexer.Pos
	IsPosition  bool
}
type plainHeaderPositionBinding struct {
	SourceField string
	Position    lexer.Pos
}

func plainHeaderSourcePositions(fn *ast.FuncDecl) map[string]lexer.Pos {
	positions := map[string]lexer.Pos{}
	for i, param := range fn.Params {
		positions[fmt.Sprintf("param[%d].Position", i)] = param.Position
	}
	if fn.ReturnType != nil {
		positions["returnType.Position"] = fn.ReturnType.Pos()
	}
	return positions
}

// Only the real signature/binder producer fields may emit a relocatable part.
func (encoder *plainDefaultProductEncoder) headerPositionField(value reflect.Value, field string) (string, error) {
	fn := encoder.product.Owner
	switch value.Type() {
	case reflect.TypeFor[SpecSignature]():
		if field != "Position" {
			return "", nil
		}
		if len(fn.Params) > 0 {
			return "param[0].Position", nil
		}
		if fn.ReturnType != nil {
			return "returnType.Position", nil
		}
		return "", nil // producer emits the exact zero position
	case reflect.TypeFor[SpecBinder]():
		if field != "SourcePos" {
			return "", nil
		}
		binder := value.Interface().(SpecBinder)
		switch binder.Kind {
		case SpecBinderParam:
			if binder.Position < 0 || binder.Position >= len(fn.Params) || binder.SourceType != fn.Params[binder.Position].Type || binder.SourceName != fn.Params[binder.Position].Name {
				return "", fmt.Errorf("header parameter position has no exact source producer")
			}
			return fmt.Sprintf("param[%d].Position", binder.Position), nil
		case SpecBinderResult:
			if fn.ReturnType == nil || binder.SourceType != fn.ReturnType {
				return "", fmt.Errorf("header result position has no exact source producer")
			}
			return "returnType.Position", nil
		default:
			return "", fmt.Errorf("unsupported header binder position producer")
		}
	}
	return "", nil
}

func splitPlainHeaderCertificate(text string, bindings []plainHeaderPositionBinding) ([]plainHeaderCertificatePart, error) {
	var parts []plainHeaderCertificatePart
	for {
		begin := strings.IndexByte(text, 0)
		if begin < 0 {
			parts = append(parts, plainHeaderCertificatePart{Text: text})
			return parts, nil
		}
		parts = append(parts, plainHeaderCertificatePart{Text: text[:begin]})
		text = text[begin+1:]
		end := strings.IndexByte(text, 0)
		if end < 0 || !strings.HasPrefix(text[:end], "position:") {
			return nil, fmt.Errorf("invalid typed header certificate framing")
		}
		index, err := strconv.Atoi(strings.TrimPrefix(text[:end], "position:"))
		if err != nil || index < 0 || index >= len(bindings) {
			return nil, fmt.Errorf("unbound typed header certificate position")
		}
		binding := bindings[index]
		parts = append(parts, plainHeaderCertificatePart{SourceField: binding.SourceField, Position: binding.Position, IsPosition: true})
		text = text[end+1:]
	}
}

func plainHeaderCertificateHash(parts []plainHeaderCertificatePart, rebind map[string]lexer.Pos) ([32]byte, error) {
	var text strings.Builder
	for _, part := range parts {
		if !part.IsPosition {
			if part.SourceField != "" || !part.Position.IsZero() {
				return [32]byte{}, fmt.Errorf("malformed literal certificate part")
			}
			text.WriteString(part.Text)
			continue
		}
		if part.Text != "" || part.SourceField == "" {
			return [32]byte{}, fmt.Errorf("malformed typed certificate part")
		}
		pos := part.Position
		if rebind != nil {
			var known bool
			pos, known = rebind[part.SourceField]
			if !known {
				return [32]byte{}, fmt.Errorf("missing exact header position producer")
			}
		}
		fmt.Fprintf(&text, "pos:%#v", pos)
	}
	return sha256.Sum256([]byte(text.String())), nil
}

func samePlainHeaderUnderSourceRebind(before, after retainedPlainHeaderContribution) bool {
	if before.Header == "" || before.Header != after.Header || len(before.Parts) == 0 || len(before.Parts) != len(after.Parts) || len(before.SourcePositions) != len(after.SourcePositions) {
		return false
	}
	oldHash, err := plainHeaderCertificateHash(before.Parts, nil)
	if err != nil || oldHash != before.Certificate {
		return false
	}
	newHash, err := plainHeaderCertificateHash(after.Parts, nil)
	if err != nil || newHash != after.Certificate {
		return false
	}
	for field := range before.SourcePositions {
		if _, known := after.SourcePositions[field]; !known {
			return false
		}
	}
	for i, part := range before.Parts {
		next := after.Parts[i]
		if part.Text != next.Text || part.IsPosition != next.IsPosition || part.SourceField != next.SourceField {
			return false
		}
		if part.IsPosition {
			oldPosition, oldKnown := before.SourcePositions[part.SourceField]
			newPosition, newKnown := after.SourcePositions[next.SourceField]
			if !oldKnown || !newKnown || part.Position != oldPosition || next.Position != newPosition {
				return false
			}
		}
	}
	rebound, err := plainHeaderCertificateHash(before.Parts, after.SourcePositions)
	return err == nil && rebound == after.Certificate
}
