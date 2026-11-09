package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func (encoder *plainDefaultProductEncoder) encode(value reflect.Value) (string, error) {
	if encoder.canonicalIdentityError != nil {
		return "", encoder.canonicalIdentityError
	}
	if !value.IsValid() {
		return "invalid", nil
	}
	value = settable(value)
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return "nil-interface", nil
		}
		return encoder.encode(value.Elem())
	}
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return "nil:" + value.Type().String(), nil
	}
	if value.CanInterface() {
		if node, ok := value.Interface().(ast.Node); ok {
			if anchor, present := encoder.anchors[node]; present {
				return anchor, nil
			}
			return "", fmt.Errorf("unowned or synthetic AST %T", node)
		}
		if symbol, ok := value.Interface().(*Symbol); ok {
			identity, err := encoder.symbolIdentity(symbol)
			if err != nil {
				return "", err
			}
			encoder.product.Symbols[symbol], _ = encoder.analyzer.lookupOptimizationSymbolIdentity(symbol)
			encoder.rememberGlobalDependency(symbol)
			fields, err := encoder.encode(reflect.ValueOf(symbol).Elem())
			if err != nil {
				return "", err
			}
			return identity + ":" + fields, nil
		}
		if site, ok := value.Interface().(indexBoundsRuntimeSite); ok {
			owner, err := encoder.encode(reflect.ValueOf(site.Owner))
			if err != nil {
				return "", err
			}
			origin, err := encoder.encode(reflect.ValueOf(site.Origin))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("runtime-site:%s:%v:%s", owner, site.Header, origin), nil
		}
		if _, ok := value.Interface().(*Scope); ok {
			return "", fmt.Errorf("scope ownership unsupported")
		}
		if typ, ok := value.Interface().(Type); ok {
			switch typ := typ.(type) {
			case *BuiltinType:
				return "builtin:" + typ.Name, nil
			case *ArrayType:
				if _, ok := typ.Elem.(*BuiltinType); !ok || !typ.HasConstSize {
					return "", fmt.Errorf("unsupported array type")
				}
			case *FuncType:
				if typ.Name != encoder.product.Owner.Name || len(typ.TypeParams)+len(typ.GenericParams)+len(typ.RegionParams)+len(typ.PermissionParams) != 0 {
					return "", fmt.Errorf("unsupported callable type")
				}
			default:
				return "", fmt.Errorf("unsupported semantic type %T", typ)
			}
		}
	}
	if value.Type() == reflect.TypeFor[lexer.Pos]() {
		pos := value.Interface().(lexer.Pos)
		if encoder.headerPositions != nil && encoder.positionSourceField != "" {
			field := encoder.positionSourceField
			expected, known := plainHeaderSourcePositions(encoder.product.Owner)[field]
			if !known || expected != pos {
				return "", fmt.Errorf("header position lacks exact inventoried producer")
			}
			index := len(*encoder.headerPositions)
			*encoder.headerPositions = append(*encoder.headerPositions, plainHeaderPositionBinding{field, pos})
			return fmt.Sprintf("\x00position:%d\x00", index), nil
		}
		if encoder.headerPositions != nil && !pos.IsZero() {
			return "", fmt.Errorf("uninventoried header position producer")
		}
		return fmt.Sprintf("pos:%#v", pos), nil
	}
	if err := validateSparseFactValueType(value); err != nil {
		return "", err
	}
	switch value.Kind() {
	case reflect.Pointer:
		key := specializationPointer{value.Type(), value.Pointer()}
		if ordinal, seen := encoder.seen[key]; seen {
			return fmt.Sprintf("reference:%d", ordinal), nil
		}
		encoder.seen[key] = len(encoder.seen)
		nested, err := encoder.encode(value.Elem())
		return "pointer:" + value.Type().String() + ":" + nested, err
	case reflect.Struct:
		var out strings.Builder
		fmt.Fprintf(&out, "%s{", value.Type())
		for i := 0; i < value.NumField(); i++ {
			prior := encoder.positionSourceField
			encoder.positionSourceField = ""
			if encoder.headerPositions != nil {
				field, err := encoder.headerPositionField(value, value.Type().Field(i).Name)
				if err != nil {
					encoder.positionSourceField = prior
					return "", err
				}
				encoder.positionSourceField = field
			}
			item, err := encoder.encode(settable(value.Field(i)))
			encoder.positionSourceField = prior
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "%s=%s;", value.Type().Field(i).Name, item)
		}
		out.WriteByte('}')
		return out.String(), nil
	case reflect.Slice, reflect.Array:
		var out strings.Builder
		fmt.Fprintf(&out, "%s[%d:", value.Type(), value.Len())
		for i := 0; i < value.Len(); i++ {
			item, err := encoder.encode(value.Index(i))
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "%s;", item)
		}
		out.WriteByte(']')
		return out.String(), nil
	case reflect.Map:
		if value.IsNil() {
			return "nil-map:" + value.Type().String(), nil
		}
		identity := specializationPointer{value.Type(), value.Pointer()}
		if ordinal, seen := encoder.seen[identity]; seen {
			return fmt.Sprintf("reference:%d", ordinal), nil
		}
		encoder.seen[identity] = len(encoder.seen)
		// Restrict map keys to scalar values or anchored AST. Pointer-order map
		// iteration must never assign canonical graph ordinals.
		keys := value.MapKeys()
		ordered := make([]string, len(keys))
		byKey := map[string]reflect.Value{}
		for i, key := range keys {
			if key.Kind() != reflect.String && key.Kind() != reflect.Int && key.Kind() != reflect.Uint && !key.Type().Implements(astNodeType) {
				return "", fmt.Errorf("unsupported map key %s", key.Type())
			}
			item, err := encoder.encode(key)
			if err != nil {
				return "", err
			}
			if _, exists := byKey[item]; exists {
				return "", fmt.Errorf("ambiguous canonical map key")
			}
			ordered[i] = item
			byKey[item] = key
		}
		sort.Strings(ordered)
		var out strings.Builder
		fmt.Fprintf(&out, "%s{", value.Type())
		for _, key := range ordered {
			item, err := encoder.encode(value.MapIndex(byKey[key]))
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "%s=%s;", key, item)
		}
		out.WriteByte('}')
		return out.String(), nil
	case reflect.String:
		text := value.String()
		var unknown string
		text = plainDefaultIdentityToken.ReplaceAllStringFunc(text, func(token string) string {
			stable, ok := encoder.tokens[token]
			if !ok {
				unknown = token
				return token
			}
			if symbol := encoder.tokenSymbols[token]; symbol != nil {
				encoder.product.Symbols[symbol] = token
				encoder.rememberGlobalDependency(symbol)
			}
			if expr := encoder.tokenExprs[token]; expr != nil {
				encoder.product.ExprIdentities[expr] = token
			}
			return "[identity:" + stable + "]"
		})
		if unknown != "" {
			return "", fmt.Errorf("unbound optimization identity %s", unknown)
		}
		return strconv.Quote(text), nil
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%s:%d", value.Type(), value.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%s:%d", value.Type(), value.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%s:%g", value.Type(), value.Float()), nil
	default:
		return "", fmt.Errorf("unsupported fact value %s", value.Type())
	}
}
