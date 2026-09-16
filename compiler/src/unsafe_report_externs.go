package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// docs/127 §3.6 — the audit surface, as one section of `-emit unsafe`.
//
// Every obligation an extern's signature creates gets a row: the parameter (or `result`), the
// KIND of obligation its type forms, and the EVIDENCE tag that discharges it. The tags are the
// four the analyzer already distinguishes for `requires` — proven, checked, assumed — plus
// `untyped`, which is not evidence at all: it means no obligation could be formed, and it is
// exactly what `-strict-externs` refuses (D1/D12 report each one at its parameter). So a family
// whose audit shows no `untyped` row is a family that passes the gate, and the report and the
// gate cannot drift apart: both read the same rule.
//
// The section is deliberately mechanical — one row per parameter, in declaration order, then the
// return — so the two compilers can be held byte-identical on it.

type externObligationRow struct {
	Name string
	Kind string
	Tag  string
}

type externObligationSummary struct {
	Name         string
	TrustedGiven bool
	TrustedWhy   string
	// Contracted mirrors checkExternContractDiscipline EXACTLY: a `requires`, `ensure`, or
	// `@trusted` satisfies it and a `uses` clause does not. That asymmetry with D12's
	// coverage rule (which does count `uses`) is stage0's, not this report's — reproducing
	// it is the point, because the header state is what decides whether -strict-externs
	// accepts the extern at all, and an audit that disagreed with the gate would be worse
	// than no audit.
	Contracted bool
	Rows       []externObligationRow
}

const (
	externObligationHandle  = "handle"
	externObligationOwned   = "owned"
	externObligationView    = "view"
	externObligationBounds  = "bounds"
	externObligationPointer = "pointer"
	externObligationScalar  = "scalar"
	externObligationVoid    = "void"

	externEvidenceProven  = "proven"
	externEvidenceChecked = "checked"
	externEvidenceAssumed = "assumed"
	externEvidenceUntyped = "untyped"
)

// writeExternObligationReport appends the §3.6 section. The two counts lead so a budget can key
// on them without parsing rows, and together they are exactly what `-strict-externs` refuses:
// an `untyped` row (D1/D12, reported at the parameter) or a `bare` extern (the older whole-extern
// rule, reported at the declaration). Both zero means the family passes the gate.
func writeExternObligationReport(out *bytes.Buffer, externs []externObligationSummary) {
	untyped, bare := 0, 0
	for _, item := range externs {
		if !item.TrustedGiven && !item.Contracted {
			bare++
		}
		for _, row := range item.Rows {
			if row.Tag == externEvidenceUntyped {
				untyped++
			}
		}
	}
	fmt.Fprintf(out, "extern-untyped: %d\n", untyped)
	fmt.Fprintf(out, "extern-bare: %d\n", bare)
	if len(externs) == 0 {
		out.WriteString("extern-obligations: none\n")
		return
	}
	out.WriteString("extern-obligations:\n")
	for _, item := range externs {
		fmt.Fprintf(out, "  %s %s:\n", item.Name, externObligationHeaderState(item))
		for _, row := range item.Rows {
			fmt.Fprintf(out, "    %s: %s %s\n", row.Name, row.Kind, row.Tag)
		}
	}
}

// externObligationHeaderState names where the extern's trust comes from: the one annotation that
// buys assumption (with its reason, so the audit lists every reason in one place), a declared
// boundary contract, or nothing at all.
func externObligationHeaderState(item externObligationSummary) string {
	if item.TrustedGiven {
		return "trusted(" + quoteExternTrustedReason(item.TrustedWhy) + ")"
	}
	if item.Contracted {
		return "contract"
	}
	return "bare"
}

// quoteExternTrustedReason escapes only a backslash and a double quote. strconv.Quote would
// also escape every non-printable and non-ASCII rune, and the second compiler would have to
// reproduce Go's exact escape table to stay byte-identical on this line. A reason is a human
// sentence; two rules is the whole grammar, and both compilers can hold it.
func quoteExternTrustedReason(reason string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(reason); i++ {
		if reason[i] == '\\' || reason[i] == '"' {
			out.WriteByte('\\')
		}
		out.WriteByte(reason[i])
	}
	out.WriteByte('"')
	return out.String()
}

func collectExternObligations(result *semantic.Result) []externObligationSummary {
	if result == nil || result.File == nil || result.GlobalScope == nil {
		return nil
	}
	var externs []externObligationSummary
	for _, decl := range result.File.Decls {
		collectExternObligationsFromDecl(result, decl, "", &externs)
	}
	sort.SliceStable(externs, func(i, j int) bool { return externs[i].Name < externs[j].Name })
	return externs
}

// collectExternObligationsFromDecl descends into namespaces, because a binding family normally
// LIVES in one: `module Net:` with the externs inside it. A top-level-only walk reported
// `extern-obligations: none` for such a file — an audit that silently omits a whole module's
// boundary, which is worse than having no audit at all. Names are namespace-qualified with "."
// to match how the `functions:` section above already prints them.
func collectExternObligationsFromDecl(result *semantic.Result, decl ast.Decl, namespace string, out *[]externObligationSummary) {
	switch node := decl.(type) {
	case *ast.StaticIfDecl:
		// A `static if` branch declares externs without introducing a namespace, and the
		// std puts its platform syscalls there (`static if ARENA_BACKEND == ...: extern
		// mmap(...)`). Walking every branch is safe BECAUSE the GlobalScope lookup below is
		// the selector: only the branch the analyzer took has symbols, so an untaken
		// branch's externs silently find nothing. Missing this left `mmap`/`munmap` out of
		// stage0's audit while its own `functions:` section listed them.
		if node == nil {
			return
		}
		for _, child := range node.Then {
			collectExternObligationsFromDecl(result, child, namespace, out)
		}
		for _, elif := range node.Elifs {
			for _, child := range elif.Body {
				collectExternObligationsFromDecl(result, child, namespace, out)
			}
		}
		for _, child := range node.Else {
			collectExternObligationsFromDecl(result, child, namespace, out)
		}
	case *ast.NamespaceDecl:
		if node == nil {
			return
		}
		next := node.Name
		if namespace != "" {
			next = namespace + "." + node.Name
		}
		for _, child := range node.Decls {
			collectExternObligationsFromDecl(result, child, next, out)
		}
	case *ast.ExternFuncDecl:
		if node == nil {
			return
		}
		qualified := node.Name
		if namespace != "" {
			qualified = namespace + "." + node.Name
		}
		sym, found := result.GlobalScope.Lookup(qualified)
		if !found || sym == nil {
			return
		}
		fnType, ok := sym.Type.(*semantic.FuncType)
		if !ok || fnType == nil {
			return
		}
		item := externObligationSummaryFor(node, fnType)
		item.Name = qualified
		*out = append(*out, item)
	}
}

func externObligationSummaryFor(fn *ast.ExternFuncDecl, fnType *semantic.FuncType) externObligationSummary {
	trustedWhy, trustedGiven := externTrustedReason(fn)
	item := externObligationSummary{
		Name:         fn.Name,
		TrustedGiven: trustedGiven,
		TrustedWhy:   trustedWhy,
		Contracted:   len(fn.Requires) > 0 || len(fn.EnsureValues) > 0 || len(fn.Ensures) > 0,
	}
	covered := externContractMentions(fn)
	boundsPointers := externBoundsPointerNames(fn)
	for i, param := range fn.Params {
		if i >= len(fnType.Params) {
			break
		}
		kind := externObligationKind(fnType.Params[i], boundsPointers[param.Name])
		item.Rows = append(item.Rows, externObligationRow{
			Name: param.Name,
			Kind: kind,
			Tag:  externEvidenceTag(kind, trustedGiven, covered[param.Name], externHasValueContract(fn)),
		})
	}
	returnKind := externObligationReturnKind(fnType.Return)
	item.Rows = append(item.Rows, externObligationRow{
		Name: "result",
		Kind: returnKind,
		// A return carries no name a contract clause could cover, so its only non-trivial
		// evidence is an `ensure`: D8 emits a runtime check for one unless `@trusted` buys
		// the assumption.
		Tag: externEvidenceTag(returnKind, trustedGiven, externHasReturnContract(fn), externHasReturnContract(fn)),
	})
	return item
}

// externObligationKind names what the TYPE promises. A handle is opaque and self-describing; a
// view carries its own length; `bounds` is the legacy (pointer, length) pair the annotation
// rewrote into one view, kept distinct so the audit shows where a length came from an
// annotation rather than from the type. `pointer` is the residue: an address with no extent.
func externObligationKind(t semantic.Type, isBoundsPointer bool) string {
	if isBoundsPointer {
		return externObligationBounds
	}
	if opt, ok := t.(*semantic.OptionalType); ok && opt != nil {
		t = opt.Value
	}
	switch typ := t.(type) {
	case *semantic.ViewType:
		return externObligationView
	case *semantic.StructType:
		if typ != nil && typ.Resource {
			return externObligationHandle
		}
		return externObligationScalar
	case *semantic.OpaqueType:
		return externObligationHandle
	case *semantic.RefType:
		// Only an `extern Name` OPAQUE referent reads as a handle. A reference to an
		// `extern resource` does NOT: isExternPointerParamType treats it as an ordinary
		// pointer, so `-strict-externs` demands contract coverage for it, and a row saying
		// `handle proven` while the gate says otherwise is the one thing this report must
		// never do. Measured, not assumed: both compilers flag `fflush(file: CFile&, ...)`.
		if typ != nil {
			if _, opaque := typ.Elem.(*semantic.OpaqueType); opaque {
				return externObligationHandle
			}
		}
		return externObligationPointer
	case *semantic.DStrType:
		// `cstr`: an unbounded NUL-terminated pointer.
		return externObligationPointer
	case *semantic.BuiltinType:
		if typ != nil && typ.Name == "void" {
			return externObligationVoid
		}
	}
	return externObligationScalar
}

// externObligationReturnKind adds `owned`: a returned handle is a resource the CALLER must
// release, which is a stronger obligation than borrowing one through a parameter (D3).
func externObligationReturnKind(t semantic.Type) string {
	if t == nil {
		return externObligationVoid
	}
	kind := externObligationKind(t, false)
	if kind == externObligationHandle {
		return externObligationOwned
	}
	return kind
}

// externEvidenceTag is the rule the report and `-strict-externs` share. `@trusted` assumes
// everything, which is the point of it: one annotation, one reason, every row marked. Otherwise
// a type that bounds its own extent is proven, a pointer named by a contract clause is checked
// at each call site, and a pointer named by nothing is untyped — the row the gate refuses.
func externEvidenceTag(kind string, trusted bool, covered bool, hasContract bool) string {
	if trusted {
		return externEvidenceAssumed
	}
	switch kind {
	case externObligationPointer:
		if covered {
			return externEvidenceChecked
		}
		return externEvidenceUntyped
	case externObligationHandle, externObligationOwned, externObligationView, externObligationBounds:
		if covered {
			return externEvidenceChecked
		}
		return externEvidenceProven
	}
	if covered && hasContract {
		return externEvidenceChecked
	}
	return externEvidenceProven
}

func externTrustedReason(fn *ast.ExternFuncDecl) (string, bool) {
	for _, annotation := range fn.Annotations {
		if annotation.Name != "trusted" {
			continue
		}
		if len(annotation.Args) > 0 {
			return annotation.Args[0], true
		}
		return "", true
	}
	return "", false
}

// externBoundsPointerNames reports the POINTER half of every `@bounds(ptr, len)` pair. The
// annotation rewrites the signature before the type is built, so the parameter already reads as
// a view; this is what keeps `bounds` distinguishable from a view the author wrote by hand.
func externBoundsPointerNames(fn *ast.ExternFuncDecl) map[string]bool {
	names := map[string]bool{}
	for _, annotation := range fn.Annotations {
		if annotation.Name != "bounds" {
			continue
		}
		for i := 0; i+1 < len(annotation.Args); i += 2 {
			names[annotation.Args[i]] = true
		}
	}
	return names
}

// externContractMentions is D12's rule verbatim: a parameter is covered when a `requires`,
// `ensure`, or `uses` clause names it. Reading the same rule here is deliberate — an audit that
// disagreed with the gate would be worse than no audit.
func externContractMentions(fn *ast.ExternFuncDecl) map[string]bool {
	mentioned := map[string]bool{}
	for _, expr := range fn.Requires {
		collectExternContractIdents(expr, mentioned)
	}
	for _, expr := range fn.EnsureValues {
		collectExternContractIdents(expr, mentioned)
	}
	for _, clause := range fn.Ensures {
		if clause.Target.Root != "" {
			mentioned[clause.Target.Root] = true
		}
	}
	for _, use := range fn.Uses {
		if use == nil {
			continue
		}
		collectExternContractIdents(use.Cond, mentioned)
		for _, expr := range use.UsesArgs {
			collectExternContractIdents(expr, mentioned)
		}
	}
	return mentioned
}

func externHasValueContract(fn *ast.ExternFuncDecl) bool {
	return len(fn.Requires) > 0 || len(fn.EnsureValues) > 0 || len(fn.Ensures) > 0 || len(fn.Uses) > 0
}

func externHasReturnContract(fn *ast.ExternFuncDecl) bool {
	for _, expr := range fn.EnsureValues {
		names := map[string]bool{}
		collectExternContractIdents(expr, names)
		if names["result"] {
			return true
		}
	}
	for _, clause := range fn.Ensures {
		if clause.Target.Root == "result" {
			return true
		}
	}
	return false
}

// collectExternContractIdents walks a contract expression for bare identifier roots. Only the
// shapes a boundary contract actually uses are handled; anything else contributes nothing,
// which is the safe direction (an unrecognised clause leaves a pointer reading `untyped`
// rather than silently claiming coverage).
func collectExternContractIdents(expr ast.Expr, out map[string]bool) {
	switch node := expr.(type) {
	case nil:
		return
	case *ast.Ident:
		if node != nil {
			out[node.Name] = true
		}
	case *ast.FieldExpr:
		if node != nil {
			collectExternContractIdents(node.Object, out)
		}
	case *ast.IndexExpr:
		if node != nil {
			collectExternContractIdents(node.Object, out)
			collectExternContractIdents(node.Index, out)
		}
	case *ast.BinaryExpr:
		if node != nil {
			collectExternContractIdents(node.Left, out)
			collectExternContractIdents(node.Right, out)
		}
	case *ast.UnaryExpr:
		if node != nil {
			collectExternContractIdents(node.Operand, out)
		}
	case *ast.ParenExpr:
		if node != nil {
			collectExternContractIdents(node.Inner, out)
		}
	case *ast.CallExpr:
		if node != nil {
			collectExternContractIdents(node.Func, out)
			for _, arg := range node.Args {
				collectExternContractIdents(arg, out)
			}
		}
	case *ast.CastExpr:
		if node != nil {
			collectExternContractIdents(node.Operand, out)
		}
	}
}

