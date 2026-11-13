//go:build cgo

package backend

import (
	"elisacore/src/semantic"
	"fmt"
	"strings"
	"unsafe"
)

func isVoidRefLikeType(t semantic.Type) bool {
	ref, ok := t.(*semantic.RefType)
	if !ok {
		return false
	}
	builtin, ok := ref.Elem.(*semantic.BuiltinType)
	return ok && builtin.Name == "void"
}
func nonVoidErrorUnion(t semantic.Type) (*semantic.ErrorUnionType, bool) {
	unionType, ok := t.(*semantic.ErrorUnionType)
	if !ok || unionType == nil || isVoidType(unionType.Value) {
		return nil, false
	}
	return unionType, true
}
func (g *llvmGenerator) resolveAssociatedTypeProjection(proj *semantic.AssociatedTypeProjection) (semantic.Type, error) {
	if proj == nil {
		return nil, fmt.Errorf("missing associated type projection")
	}
	if g == nil || g.result == nil {
		return nil, fmt.Errorf("missing semantic result for associated type projection %s", proj.String())
	}
	resolved, ok := semantic.ResolveAssociatedTypeProjection(proj, g.result.StaticImpls)
	if !ok || resolved == nil {
		return nil, fmt.Errorf("unresolved associated type projection %s", proj.String())
	}
	return resolved, nil
}
func (g *llvmGenerator) noteType(t semantic.Type) error {
	if t == nil {
		return nil
	}
	key := g.typeMemoKey(t)
	if g.noteTypeDone[key] || g.noteTypeInProgress[key] {
		return nil
	}
	g.noteTypeInProgress[key] = true
	defer delete(g.noteTypeInProgress, key)
	var err error
	switch tt := t.(type) {
	case *semantic.InvalidType, *semantic.NeverType, *semantic.NullType, *semantic.BuiltinType, *semantic.BitIntType, *semantic.TypeParamType, *semantic.CStrType, *semantic.ErrorSetType:
		err = nil
	case *semantic.AssociatedTypeProjection:
		resolved, resolveErr := g.resolveAssociatedTypeProjection(tt)
		if resolveErr != nil {
			err = resolveErr
			break
		}
		err = g.noteType(resolved)
	case *semantic.ConstEnumType:
		err = g.noteType(tt.Storage)
	case *semantic.PackedVariantViewType:
		if err = g.noteType(tt.Enum); err != nil {
			break
		}
		if tt.Enum != nil && tt.Enum.StoreType != nil {
			if err = g.noteType(tt.Enum.StoreType); err != nil {
				break
			}
		}
		_, err = g.ensurePackedVariantViewCarrierType(tt)
	case *semantic.PackedEnumStoreType:
		_, err = g.lowerPackedEnumStoreType(tt)
	case *semantic.ErrorUnionType:
		if err = g.noteType(tt.Value); err != nil {
			break
		}
		err = g.noteType(tt.Errors)
	case *semantic.OptionalType:
		err = g.noteType(tt.Value)
	case *semantic.TupleType:
		for _, field := range tt.Fields {
			if err = g.noteType(field.Type); err != nil {
				break
			}
		}
	case *semantic.SViewType:
		if st, ok := g.lookupStructType("StringView"); ok {
			_, err = g.ensureStructBody(st.Name, st)
			break
		}
		err = fmt.Errorf("missing runtime struct StringView")
	case *semantic.RefType:
		err = g.noteType(tt.Elem)
	case *semantic.ArrayType:
		err = g.noteType(tt.Elem)
	case *semantic.DArrayType:
		_, err = g.ensureRuntimeDynArray(tt.Elem)
	case *semantic.ViewType:
		_, err = g.ensureRuntimeDynArrayView()
	case *semantic.DictType:
		if err = g.noteType(tt.Key); err != nil {
			break
		}
		if err = g.noteType(tt.Value); err != nil {
			break
		}
		base, ok := g.lookupNamedType("DynDict")
		if !ok {
			err = fmt.Errorf("missing runtime struct DynDict")
			break
		}
		_, err = g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynDict", Base: base, Args: []semantic.Type{tt.Key, tt.Value}})
	case *semantic.SetType:
		if err = g.noteType(tt.Elem); err != nil {
			break
		}
		base, ok := g.lookupNamedType("DynSet")
		if !ok {
			err = fmt.Errorf("missing runtime struct DynSet")
			break
		}
		_, err = g.ensureGenericInstanceStruct(&semantic.GenericInstanceType{Name: "DynSet", Base: base, Args: []semantic.Type{tt.Elem}})
	case *semantic.EnumType:
		if tt.Decl != nil {
			for _, fieldDecl := range tt.Decl.Common {
				field, ok := tt.Common[fieldDecl.Name]
				if !ok {
					continue
				}
				if err = g.noteType(field.Type); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if err = g.noteType(payload); err != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			if tt.Packed {
				_, err = g.ensurePackedEnumStorageType(tt)
			} else {
				_, err = g.ensureEnumBody(tt.Name, tt)
			}
		}
	case *semantic.StructType:
		if len(structGenericParams(tt)) == 0 {
			_, err = g.ensureStructBody(tt.Name, tt)
			break
		}
		_, err = g.ensureNamedStructType(tt.Name)
	case *semantic.AggregateStateType:
		err = g.noteType(tt.Base)
	case *semantic.IDType:
		err = g.noteType(tt.Storage)
	case *semantic.AddressSpaceType:
		err = g.noteType(tt.Storage)
	case *semantic.OpaqueType:
		_, err = g.ensureNamedStructType(tt.Name)
	case *semantic.GenericInstanceType:
		_, err = g.ensureGenericInstanceStruct(tt)
	case *semantic.FuncType:
		for _, param := range tt.Params {
			if err = g.noteType(param); err != nil {
				break
			}
		}
		if err == nil {
			err = g.noteType(tt.Return)
		}
	default:
		err = fmt.Errorf("unsupported semantic type %T", t)
	}
	if err == nil {
		g.noteTypeDone[key] = true
	}
	return err
}
func (g *llvmGenerator) ensureFunctionDeclared(name string, fn *semantic.FuncType) (llvmIRValueRef, error) {
	if value, ok := g.functions[name]; ok {
		if err := g.ensureDeclaredFunctionType(name, value, fn); err != nil {
			return nil, err
		}
		if err := g.importPartitionFunction(name, fn, value); err != nil {
			return nil, err
		}
		return value, nil
	}
	value, err := g.addFunction(name, fn)
	if err != nil {
		return nil, err
	}
	g.functions[name] = value
	if err := g.importPartitionFunction(name, fn, value); err != nil {
		return nil, err
	}
	return value, nil
}
func (g *llvmGenerator) llvmSymbolName(name string) string {
	if g == nil || g.result == nil || !g.hasGlobalScope() {
		return name
	}
	if impl := g.sameNameExportImplSymbol(name); impl != "" {
		return impl
	}
	sym, ok := g.lookupGlobal(name)
	if !ok || sym == nil || strings.TrimSpace(sym.LinkName) == "" {
		return name
	}
	return sym.LinkName
}
func (g *llvmGenerator) ensureDeclaredFunctionType(name string, value llvmIRValueRef, fn *semantic.FuncType) error {
	if g == nil || value == nil || fn == nil {
		return nil
	}
	expectedType, err := g.lowerFunctionTypeForSymbol(name, fn)
	if err != nil {
		return err
	}
	actualType := llvmGlobalGetValueType(value)
	if actualType == expectedType {
		return nil
	}
	actualText := disposeLLVMMessage(llvmPrintTypeToString(actualType), "<unknown>")
	expectedText := disposeLLVMMessage(llvmPrintTypeToString(expectedType), "<unknown>")
	if actualText == expectedText {
		return nil
	}
	return fmt.Errorf("conflicting LLVM function declaration for %q: existing %s, requested %s", name, actualText, expectedText)
}
func (g *llvmGenerator) ensureGlobalDeclared(name string, t semantic.Type, external bool) (llvmIRValueRef, error) {
	if value, ok := g.globals[name]; ok {
		return value, nil
	}
	value, err := g.addGlobal(name, t, external)
	if err != nil {
		return nil, err
	}
	g.globals[name] = value
	return value, nil
}
func (g *llvmGenerator) addFunction(name string, fn *semantic.FuncType) (llvmIRValueRef, error) {
	lookupName := name
	if fn != nil && strings.TrimSpace(fn.IntrinsicName) != "" {
		lookupName = strings.TrimSpace(fn.IntrinsicName)
	}
	if builtin, ok := g.elisaBuiltinIntrinsicFunction(lookupName); ok {
		return builtin, nil
	}
	if intrinsicID, overloadedParamTypes, ok, err := g.lookupIntrinsic(lookupName, fn); err != nil {
		return nil, err
	} else if ok {
		return llvmGetIntrinsicDeclaration(g.module, intrinsicID, llvmTypeSlicePtr(overloadedParamTypes), llvmIRSize(len(overloadedParamTypes))), nil
	}

	fnType, err := g.lowerFunctionTypeForSymbol(name, fn)
	if err != nil {
		return nil, err
	}
	symbolName := g.llvmSymbolName(name)
	nameC := cString(symbolName)
	defer llvmFree(unsafe.Pointer(nameC))
	// Two externs can resolve to the same linker symbol — e.g. a plain
	// `extern mprotect` and a `@link_name(mprotect)`-aliased `ge_mprotect`.
	// LLVMAddFunction would rename the second to "mprotect.1", which on a
	// Mach-O cross-build under `-undefined,dynamic_lookup` binds to NULL
	// (there is no such libc symbol) and crashes when called. Reuse the
	// existing global for the symbol so both names share one import; with
	// opaque pointers, call sites supply their own lowered function type.
	if existing := llvmGetNamedFunction(g.module, nameC); existing != nil {
		return existing, nil
	}
	value := llvmAddFunction(g.module, nameC, fnType)
	llvmSetLinkage(value, llvmExternalLinkage)
	if callConv, ok := g.llvmCallConvForFunc(fn); ok {
		llvmSetFunctionCallConv(value, callConv)
	}
	if module := g.wasmImportModule(name); module != "" {
		g.addFunctionStringAttribute(value, "wasm-import-module", module)
	}
	g.applyAggregateAbiAttrs(value, fn)
	g.applyLibcNoreturnAttr(value, name, symbolName, fnType)
	return value, nil
}
