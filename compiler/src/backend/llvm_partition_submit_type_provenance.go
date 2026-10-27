//go:build cgo

package backend

import (
	"reflect"
	"strings"

	"elisacore/src/semantic"
)

// submitTypeProvenance recognizes only capture identities registered by the
// current generator. A registered object must have one immutable layout for its
// lifetime; a clone with the same name is tainted but is not authenticated.
type submitTypeProvenance struct {
	captures map[submitCaptureName]*semantic.StructType
}

type submitCaptureName struct{ namespace, name string }

func (p *submitTypeProvenance) register(t *semantic.StructType) bool {
	if t == nil || t.Name == "" {
		return false
	}
	if p.captures == nil {
		p.captures = make(map[submitCaptureName]*semantic.StructType)
	}
	k := submitCaptureName{t.Namespace, t.Name}
	if old, ok := p.captures[k]; ok && old != t {
		return false
	}
	p.captures[k] = t
	return true
}

// classify returns independent taint and safety results. Even an untainted
// graph must be rejected if safety is false: it may contain an unknown edge.
// safety authenticates the encoding of every capture occurrence, not merely the
// presence of a namespace somewhere in the key. It does not check symbol-name
// collisions, linkage, or runtime ABI, which remain the caller's responsibility.
func (p *submitTypeProvenance) classify(bindings []semantic.Type) (tainted, safe bool) {
	safe = true
	if _, err := semantic.TypeSymbolKeys(bindings); err != nil {
		safe = false
	}
	active := make(map[semantic.Type]bool)
	var visit func(semantic.Type, bool, int)
	visit = func(t semantic.Type, encoded bool, depth int) {
		if t == nil || reflect.ValueOf(t).Kind() == reflect.Ptr && reflect.ValueOf(t).IsNil() || depth >= 256 {
			safe = false
			return
		}
		if active[t] {
			safe = false
			return
		}
		active[t] = true
		defer delete(active, t)
		child := func(x semantic.Type) { visit(x, encoded, depth+1) }
		hidden := func(x semantic.Type) { visit(x, false, depth+1) }
		switch x := t.(type) {
		case *semantic.StructType:
			registered, knownCapture := p.captures[submitCaptureName{x.Namespace, x.Name}]
			if !knownCapture && strings.HasPrefix(x.Name, "elisa.partition.") {
				// A synthetic-looking nominal identity from another generator or
				// an unregistered clone has no authenticated local layout.
				tainted, safe = true, false
			}
			if knownCapture {
				tainted = true
				if !encoded || registered != x {
					safe = false
				}
				// The registered, immutable capture is a unique nominal leaf.
				if registered == x {
					return
				}
			}
			// A nominal generic name can hide phantom type arguments even if no
			// field retains them. Its hash is not a transitive provenance proof.
			if len(x.TypeParams) != 0 || len(x.GenericParams) != 0 || strings.Contains(x.Name, "$generic_v1$") {
				safe = false
			}
			for _, f := range x.Fields {
				hidden(f.Type)
			}
		case *semantic.EnumType:
			if x.StoreType != nil {
				hidden(x.StoreType)
			}
			for _, f := range x.Common {
				hidden(f.Type)
			}
			for _, v := range x.Variants {
				if v == nil {
					safe = false
					continue
				}
				for _, q := range v.Payload {
					hidden(q)
				}
			}
			for _, v := range x.VariantMap {
				if v == nil {
					safe = false
					continue
				}
				for _, q := range v.Payload {
					hidden(q)
				}
			}
			if x.Parent != nil {
				hidden(x.Parent)
			}
			for _, c := range x.Children {
				hidden(c)
			}
		case *semantic.IDType:
			child(x.Tag)
			child(x.Storage)
		case *semantic.AddressSpaceType:
			child(x.Elem)
			child(x.Storage)
		case *semantic.ConstParamType:
			safe = false
			child(x.ValueType)
		case *semantic.ErrorSetType:
			for _, values := range x.Payloads {
				for _, v := range values {
					child(v)
				}
			}
		case *semantic.ErrorUnionType:
			child(x.Value)
			child(x.Errors)
		case *semantic.OptionalType:
			child(x.Value)
		case *semantic.TupleType:
			for _, f := range x.Fields {
				child(f.Type)
			}
		case *semantic.BitGroupType:
			for _, f := range x.Members {
				child(f.Type)
			}
		case *semantic.RefType:
			child(x.Elem)
		case *semantic.ArrayType:
			child(x.Elem)
		case *semantic.DArrayType:
			child(x.Elem)
		case *semantic.ViewType:
			child(x.Elem)
		case *semantic.StoreRowsViewType:
			child(x.Store)
		case *semantic.StoreRowViewType:
			child(x.Store)
		case *semantic.DictType:
			child(x.Key)
			child(x.Value)
		case *semantic.SetType:
			child(x.Elem)
		case *semantic.DictEntryType:
			child(x.Dict)
		case *semantic.PackedEnumStoreType:
			child(x.State)
			if x.Enum != nil {
				hidden(x.Enum)
			}
		case *semantic.PackedVariantViewType:
			child(x.Enum)
			if x.Variant == nil {
				safe = false
			} else {
				for _, v := range x.Variant.Payload {
					hidden(v)
				}
			}
		case *semantic.GenericInstanceType:
			child(x.Base)
			for _, a := range x.Args {
				child(a)
			}
		case *semantic.AggregateStateType:
			child(x.Base)
		case *semantic.FuncType:
			for _, a := range x.Params {
				child(a)
			}
			child(x.Return)
		case *semantic.TypeParamType, *semantic.StructStateCaseType, *semantic.StructStateSetType:
			safe = false
		case *semantic.NeverType, *semantic.NullType, *semantic.BuiltinType, *semantic.BitIntType,
			*semantic.ConstValueType, *semantic.RefStorageValueType, *semantic.RegionParamType,
			*semantic.RegionValueType, *semantic.ConstEnumType, *semantic.CStrType,
			*semantic.SViewType, *semantic.OpaqueType:
		default:
			safe = false
		}
	}
	for _, binding := range bindings {
		visit(binding, true, 0)
	}
	return
}
