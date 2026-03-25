package backend

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"llcontext/src/semantic"
)

type publicAggregate struct {
	Name       string
	Type       semantic.Type
	StrongDeps []string
}

func GenerateCHeader(result *semantic.Result) (string, error) {
	if result == nil || result.File == nil {
		return "", fmt.Errorf("backend requires a semantic result with file metadata")
	}
	aggregates, err := collectPublicAggregates(result)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	guard := headerGuardName(result.File.Filename)
	out.WriteString("#ifndef ")
	out.WriteString(guard)
	out.WriteString("\n#define ")
	out.WriteString(guard)
	out.WriteString("\n\n#include <stdint.h>\n\n")
	out.WriteString("#ifdef __cplusplus\nextern \"C\" {\n#endif\n\n")

	for _, agg := range aggregates {
		out.WriteString("typedef struct ")
		out.WriteString(agg.Name)
		out.WriteByte(' ')
		out.WriteString(agg.Name)
		out.WriteString(";\n")
	}
	if len(aggregates) > 0 {
		out.WriteByte('\n')
	}

	for _, agg := range aggregates {
		fields, err := exportedStructFields(agg.Type)
		if err != nil {
			return "", err
		}
		out.WriteString("struct ")
		out.WriteString(agg.Name)
		out.WriteString(" {\n")
		for _, field := range fields {
			decl, err := formatCDecl(field.Type, field.Name, result)
			if err != nil {
				return "", fmt.Errorf("failed to render field %s.%s: %w", agg.Name, field.Name, err)
			}
			out.WriteString("    ")
			out.WriteString(decl)
			out.WriteString(";\n")
		}
		out.WriteString("}")
		out.WriteString(cAlignmentAttributeSuffix(agg.Type))
		out.WriteString(";\n\n")
	}

	for _, exported := range result.ExportedGlobals {
		if exported == nil {
			continue
		}
		decl, err := formatGlobalDeclaration(exported, result)
		if err != nil {
			return "", err
		}
		out.WriteString(decl)
		out.WriteString(";\n")
	}
	if len(result.ExportedGlobals) > 0 {
		out.WriteByte('\n')
	}

	for _, exported := range result.ExportedFuncs {
		if exported == nil || exported.Signature == nil {
			continue
		}
		proto, err := formatFunctionPrototype(exported.PublicName, exported.Signature, result)
		if err != nil {
			return "", err
		}
		out.WriteString(proto)
		out.WriteString(";\n")
	}
	if len(result.ExportedFuncs) > 0 {
		out.WriteByte('\n')
	}

	out.WriteString("#ifdef __cplusplus\n}\n#endif\n\n#endif\n")
	return out.String(), nil
}

func collectPublicAggregates(result *semantic.Result) ([]publicAggregate, error) {
	aggregates := map[string]*publicAggregate{}
	for _, exported := range result.ExportedTypes {
		if exported == nil {
			continue
		}
		if err := ensurePublicAggregate(exported.Type, result, aggregates); err != nil {
			return nil, err
		}
	}
	for _, exported := range result.ExportedFuncs {
		if exported == nil || exported.Signature == nil {
			continue
		}
		for _, param := range exported.Signature.Params {
			if err := registerAggregateDependencies(param, result, aggregates); err != nil {
				return nil, err
			}
		}
		if err := registerAggregateDependencies(exported.Signature.Return, result, aggregates); err != nil {
			return nil, err
		}
	}
	for _, exported := range result.ExportedGlobals {
		if exported == nil {
			continue
		}
		if err := registerAggregateDependencies(exported.Type, result, aggregates); err != nil {
			return nil, err
		}
	}

	names := make([]string, 0, len(aggregates))
	for name := range aggregates {
		names = append(names, name)
	}
	sort.Strings(names)
	ordered := make([]publicAggregate, 0, len(names))
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visited[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("header generation found a recursive by-value aggregate dependency involving %s", name)
		}
		agg, ok := aggregates[name]
		if !ok {
			return nil
		}
		visiting[name] = true
		deps := append([]string(nil), agg.StrongDeps...)
		sort.Strings(deps)
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visiting[name] = false
		visited[name] = true
		ordered = append(ordered, *agg)
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func registerAggregateDependencies(t semantic.Type, result *semantic.Result, aggregates map[string]*publicAggregate) error {
	switch tt := t.(type) {
	case nil, *semantic.BuiltinType:
		return nil
	case *semantic.RefType:
		return registerAggregateDependencies(tt.Elem, result, aggregates)
	case *semantic.ArrayType:
		return registerAggregateDependencies(tt.Elem, result, aggregates)
	case *semantic.StructType, *semantic.GenericInstanceType:
		return ensurePublicAggregate(tt, result, aggregates)
	default:
		return nil
	}
}

func ensurePublicAggregate(t semantic.Type, result *semantic.Result, aggregates map[string]*publicAggregate) error {
	name, ok := publicTypeNameForHeader(t, result)
	if !ok {
		return fmt.Errorf("header generation requires a public exported name for %s", t.String())
	}
	if name == "" {
		return nil
	}
	if _, ok := aggregates[name]; ok {
		return nil
	}
	fields, err := exportedStructFields(t)
	if err != nil {
		return err
	}
	agg := &publicAggregate{Name: name, Type: t}
	aggregates[name] = agg
	strongDeps := map[string]bool{}
	for _, field := range fields {
		deps, err := directAggregateDefinitionDeps(field.Type, result)
		if err != nil {
			return err
		}
		for _, dep := range deps {
			if dep != name {
				strongDeps[dep] = true
			}
		}
		if err := registerAggregateDependencies(field.Type, result, aggregates); err != nil {
			return err
		}
	}
	for dep := range strongDeps {
		agg.StrongDeps = append(agg.StrongDeps, dep)
	}
	sort.Strings(agg.StrongDeps)
	return nil
}

func directAggregateDefinitionDeps(t semantic.Type, result *semantic.Result) ([]string, error) {
	deps := map[string]bool{}
	if err := collectDirectAggregateDefinitionDeps(t, result, deps, false); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func collectDirectAggregateDefinitionDeps(t semantic.Type, result *semantic.Result, deps map[string]bool, allowIncompleteStruct bool) error {
	switch tt := t.(type) {
	case nil, *semantic.BuiltinType:
		return nil
	case *semantic.StructType, *semantic.GenericInstanceType:
		if allowIncompleteStruct {
			return nil
		}
		name, ok := publicTypeNameForHeader(tt, result)
		if !ok {
			return fmt.Errorf("header generation requires a public exported name for %s", tt.String())
		}
		deps[name] = true
		return nil
	case *semantic.RefType:
		switch tt.Elem.(type) {
		case *semantic.StructType, *semantic.GenericInstanceType:
			return nil
		default:
			return collectDirectAggregateDefinitionDeps(tt.Elem, result, deps, true)
		}
	case *semantic.ArrayType:
		return collectDirectAggregateDefinitionDeps(tt.Elem, result, deps, false)
	default:
		return nil
	}
}

func publicTypeNameForHeader(t semantic.Type, result *semantic.Result) (string, bool) {
	for _, exported := range result.ExportedTypes {
		if exported != nil && semantic.SameType(exported.Type, t) {
			return exported.PublicName, true
		}
	}
	switch tt := t.(type) {
	case *semantic.StructType:
		return tt.Name, true
	case *semantic.GenericInstanceType:
		return "", false
	default:
		return "", false
	}
}

func exportedStructFields(t semantic.Type) ([]semantic.Field, error) {
	switch tt := t.(type) {
	case *semantic.StructType:
		if tt.Decl == nil {
			return nil, fmt.Errorf("struct %s is missing declaration metadata", tt.Name)
		}
		fields := make([]semantic.Field, 0, len(tt.Decl.Fields))
		for _, fieldDecl := range tt.Decl.Fields {
			field, ok := tt.Fields[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing semantic field %s.%s", tt.Name, fieldDecl.Name)
			}
			fields = append(fields, field)
		}
		return fields, nil
	case *semantic.GenericInstanceType:
		base, ok := tt.Base.(*semantic.StructType)
		if !ok || base.Decl == nil {
			return nil, fmt.Errorf("generic instance %s is not struct-backed", tt.String())
		}
		params := structGenericParams(base)
		if len(params) != len(tt.Args) {
			return nil, fmt.Errorf("generic instance %s has %d args, expected %d", tt.Name, len(tt.Args), len(params))
		}
		bindings := genericBindingsForArgs(params, tt.Args)
		fields := make([]semantic.Field, 0, len(base.Decl.Fields))
		for _, fieldDecl := range base.Decl.Fields {
			field, ok := base.Fields[fieldDecl.Name]
			if !ok {
				return nil, fmt.Errorf("missing semantic field %s.%s", base.Name, fieldDecl.Name)
			}
			fields = append(fields, semantic.Field{Name: field.Name, Type: substituteHeaderType(field.Type, bindings), Mutable: field.Mutable, IsTail: field.IsTail})
		}
		return fields, nil
	default:
		return nil, fmt.Errorf("header generation expected a concrete struct type, got %s", t.String())
	}
}

func substituteHeaderType(t semantic.Type, bindings map[string]semantic.Type) semantic.Type {
	if len(bindings) == 0 || t == nil {
		return t
	}
	switch tt := t.(type) {
	case *semantic.TypeParamType:
		if bound, ok := bindings[tt.Name]; ok {
			return bound
		}
		return tt
	case *semantic.RefStorageParamType:
		if bound, ok := bindings[tt.Name]; ok {
			return bound
		}
		return tt
	case *semantic.RefStateParamType:
		if bound, ok := bindings[tt.Name]; ok {
			return bound
		}
		return tt
	case *semantic.RefType:
		state := tt.State
		stateParam := tt.StateParam
		if stateParam != "" {
			if bound, ok := bindings[stateParam]; ok {
				switch bound := bound.(type) {
				case *semantic.RefStateValueType:
					state = bound.State
					stateParam = ""
				case *semantic.RefStateParamType:
					stateParam = bound.Name
				}
			}
		}
		storage := tt.Storage
		storageParam := tt.StorageParam
		if storageParam != "" {
			if bound, ok := bindings[storageParam]; ok {
				switch bound := bound.(type) {
				case *semantic.RefStorageValueType:
					storage = bound.Storage
					storageParam = ""
				case *semantic.RefStorageParamType:
					storageParam = bound.Name
				}
			}
		}
		return &semantic.RefType{Elem: substituteHeaderType(tt.Elem, bindings), State: state, StateParam: stateParam, Storage: storage, StorageParam: storageParam, Region: tt.Region, ExplicitStorage: tt.ExplicitStorage}
	case *semantic.ArrayType:
		return &semantic.ArrayType{Elem: substituteHeaderType(tt.Elem, bindings), Size: tt.Size, HasConstSize: tt.HasConstSize, ConstSize: tt.ConstSize, SurfaceName: tt.SurfaceName}
	case *semantic.GenericInstanceType:
		args := make([]semantic.Type, 0, len(tt.Args))
		for _, arg := range tt.Args {
			args = append(args, substituteHeaderType(arg, bindings))
		}
		return &semantic.GenericInstanceType{Name: tt.Name, Base: tt.Base, Args: args}
	default:
		return t
	}
}

func formatFunctionPrototype(name string, fn *semantic.FuncType, result *semantic.Result) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("missing function type for exported function %s", name)
	}
	params := make([]string, 0, len(fn.Params))
	for i, param := range fn.Params {
		decl, err := formatCDecl(param, fmt.Sprintf("arg%d", i), result)
		if err != nil {
			return "", fmt.Errorf("failed to render parameter %d for %s: %w", i+1, name, err)
		}
		params = append(params, decl)
	}
	paramList := "void"
	if len(params) > 0 {
		paramList = strings.Join(params, ", ")
	}
	return formatCDecl(fn.Return, name+"("+paramList+")", result)
}

func formatGlobalDeclaration(exported *semantic.ExportedGlobal, result *semantic.Result) (string, error) {
	if exported == nil {
		return "", fmt.Errorf("missing exported global metadata")
	}
	decl, err := formatCDecl(exported.Type, exported.PublicName, result)
	if err != nil {
		return "", fmt.Errorf("failed to render exported global %s: %w", exported.PublicName, err)
	}
	return "extern " + decl + cAlignmentAttributeSuffix(exported.Type), nil
}

func cAlignmentAttributeSuffix(t semantic.Type) string {
	alignment, ok := semantic.RequestedAlignment(t)
	if !ok || alignment <= 0 {
		return ""
	}
	return fmt.Sprintf(" __attribute__((aligned(%d)))", alignment)
}

func formatCDecl(t semantic.Type, name string, result *semantic.Result) (string, error) {
	switch tt := t.(type) {
	case nil:
		if strings.TrimSpace(name) == "" {
			return "void", nil
		}
		return "void " + name, nil
	case *semantic.BuiltinType:
		base, err := cBuiltinTypeName(tt.Name)
		if err != nil {
			return "", err
		}
		if name == "" {
			return base, nil
		}
		return base + " " + name, nil
	case *semantic.RefType:
		nextName := "*" + name
		if _, ok := tt.Elem.(*semantic.ArrayType); ok {
			nextName = "(*" + name + ")"
		}
		return formatCDecl(tt.Elem, nextName, result)
	case *semantic.ArrayType:
		if !tt.HasConstSize {
			return "", fmt.Errorf("array declaration %s is missing a compile-time size", tt.String())
		}
		return formatCDecl(tt.Elem, fmt.Sprintf("%s[%d]", name, tt.ConstSize), result)
	case *semantic.StructType, *semantic.GenericInstanceType:
		publicName, ok := publicTypeNameForHeader(tt, result)
		if !ok {
			return "", fmt.Errorf("type %s needs an exported public name for header emission", tt.String())
		}
		if name == "" {
			return publicName, nil
		}
		return publicName + " " + name, nil
	default:
		return "", fmt.Errorf("unsupported C header type %s", t.String())
	}
}

func cBuiltinTypeName(name string) (string, error) {
	switch name {
	case "void":
		return "void", nil
	case "char":
		return "int64_t", nil
	case "i8":
		return "int8_t", nil
	case "u8":
		return "uint8_t", nil
	case "i16":
		return "int16_t", nil
	case "u16":
		return "uint16_t", nil
	case "i32":
		return "int32_t", nil
	case "u32":
		return "uint32_t", nil
	case "i64":
		return "int64_t", nil
	case "u64":
		return "uint64_t", nil
	case "f32":
		return "float", nil
	case "f64":
		return "double", nil
	case "int", "isize":
		return "intptr_t", nil
	case "usize", "uintptr":
		return "uintptr_t", nil
	default:
		return "", fmt.Errorf("unsupported builtin C header type %q", name)
	}
}

func headerGuardName(filename string) string {
	base := strings.ToUpper(filepath.Base(filename))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var out strings.Builder
	for _, r := range base {
		switch {
		case r >= 'A' && r <= 'Z':
			out.WriteRune(r)
		case r >= '0' && r <= '9':
			out.WriteRune(r)
		default:
			out.WriteByte('_')
		}
	}
	value := strings.Trim(out.String(), "_")
	if value == "" {
		value = "LLCONTEXT_EXPORT"
	}
	return value + "_H"
}
