package easm

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// selectPlatformImpls keeps only the platform-tagged functions whose `for <platform>` matches
// the build target, and checks that any kept impl named after a protocol method conforms to
// its signature. Dropping the non-matching impls before VerifyModule is what lets two impls
// share a name (e.g. read_fenced for x86_64 / for aarch64) without a duplicate-export clash —
// the collapse of the parallel common_x86_64 / common_aarch64 files into one protocol.
func selectPlatformImpls(module *Module) []Issue {
	if module == nil {
		return nil
	}
	var issues []Issue
	methods := map[string]protocolMethod{}
	for i := range module.Protocols {
		for _, m := range module.Protocols[i].Methods {
			methods[m.Name] = m
		}
	}
	var kept []Function
	for _, fn := range module.Functions {
		if fn.Platform != "" && !platformMatchesTarget(fn.Platform, module.Target) {
			continue
		}
		if sig, ok := methods[fn.Name]; ok {
			if len(fn.Params) != sig.Arity || strings.TrimSpace(fn.ReturnType) != strings.TrimSpace(sig.Return) {
				issues = append(issues, Issue{Severity: "error", Code: "protocol-conformance", File: module.Path, Line: fn.Line, Message: fmt.Sprintf("impl %s does not match protocol method signature (params %d vs %d, return %q vs %q)", fn.Name, len(fn.Params), sig.Arity, strings.TrimSpace(fn.ReturnType), strings.TrimSpace(sig.Return))})
			}
		}
		kept = append(kept, fn)
	}
	module.Functions = kept
	return issues
}

func platformMatchesTarget(platform string, target string) bool {
	platform = strings.ToLower(strings.TrimSpace(platform))
	platform = strings.TrimSuffix(strings.TrimPrefix(platform, "("), ")")
	target = strings.ToLower(strings.TrimSpace(target))
	for _, key := range strings.Split(platform, ",") {
		if key = strings.TrimSpace(key); key == "" || key == "any" {
			continue
		}
		if !platformKeyMatches(key, target) {
			return false
		}
	}
	return true
}

// DerivedEffects projects a function's caller-facing Unsafe.* / Segment.* effect set from its
// verified `requires:` contract and segment-state, per docs/101 §4. It is the honest, drift-proof
// alternative to hand-authored can[...]: the hazard is stated once, in the contract the verifier
// already checks, so the effect cannot disagree with what the instructions do.
//
// This is the COMPLETE set, used for reporting and migration planning. The backend
// (easmDeclaredEffectPermissions) currently ENFORCES only a subset against the matching Elisa
// extern's can[...]; widening enforcement onto this full set is a coordinated migration that must
// add the new permissions to those extern declarations, so it is intentionally not wired here.
func DerivedEffects(fn *Function) []string {
	if fn == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(e string) {
		if e != "" && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	// Floor: any raw EASM routine is outside Elisa's value semantics.
	add("Unsafe.RawExtern")
	for _, req := range fn.Requires {
		switch strings.TrimSpace(req) {
		case "x86_64.segment.write":
			add("Unsafe.SegmentMutation")
		case "control.indirect", "control.target.untyped":
			add("Unsafe.IndirectCall")
		case "memory.base.untyped":
			add("Unsafe.PointerCast")
		}
	}
	switch finalFSOwner(fn) {
	case "guest":
		add("Unsafe.GuestSegmentInstall")
		add("Segment.Guest")
	case "host":
		add("Segment.Host")
	}
	return out
}

func finalFSOwner(fn *Function) string {
	owner := ""
	for _, inst := range fn.Instructions {
		if !inst.Pseudo || strings.ToLower(strings.TrimSpace(inst.Op)) != "state" {
			continue
		}
		text := strings.TrimSpace(inst.Text)
		if !strings.HasPrefix(strings.ToLower(text), "fs:") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(text[len("fs:"):]))
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "guest":
			owner = "guest"
		case "host":
			owner = "host"
		}
	}
	return owner
}

func platformKeyMatches(key string, target string) bool {
	switch key {
	case "x86_64", "amd64", "x64":
		return strings.Contains(target, "x86_64") || strings.Contains(target, "amd64")
	case "aarch64", "arm64":
		return strings.Contains(target, "aarch64") || strings.Contains(target, "arm64")
	case "darwin", "macos", "apple":
		return strings.Contains(target, "darwin") || strings.Contains(target, "apple") || strings.Contains(target, "macos")
	case "linux":
		return strings.Contains(target, "linux")
	case "windows", "win32":
		return strings.Contains(target, "windows")
	default:
		// A role axis (host/guest) or any key not encoded in the target triple is treated as
		// non-discriminating for now; build-level role dispatch is a documented follow-up.
		return true
	}
}

func parseFunctionHeader(path string, line int, text string) (Function, *Issue) {
	m := exportHeaderRE.FindStringSubmatch(text)
	if m == nil {
		return Function{}, &Issue{Severity: "error", Code: "invalid-export", File: path, Line: line, Message: "invalid export def header"}
	}
	fn := Function{Name: m[1], ReturnType: m[3], Line: line}
	seenParams := map[string]bool{}
	for _, part := range splitCSV(m[2]) {
		if part == "" {
			continue
		}
		pieces := strings.SplitN(part, ":", 2)
		if len(pieces) != 2 {
			return fn, &Issue{Severity: "error", Code: "invalid-param", File: path, Line: line, Message: "expected parameter as name: type"}
		}
		name := strings.TrimSpace(pieces[0])
		if seenParams[name] {
			return fn, &Issue{Severity: "error", Code: "duplicate-param", File: path, Line: line, Message: fmt.Sprintf("duplicate EASM parameter %s", name)}
		}
		seenParams[name] = true
		fn.Params = append(fn.Params, Param{Name: name, Type: strings.TrimSpace(pieces[1])})
	}
	tail := strings.TrimSpace(m[4])
	if strings.HasPrefix(tail, "abi ") {
		fields := strings.Fields(tail)
		if len(fields) >= 2 {
			fn.ABI = fields[1]
		}
	}
	if i := strings.Index(tail, "can["); i >= 0 {
		end := strings.Index(tail[i:], "]")
		if end >= 0 {
			fn.Effects = splitCSV(tail[i+len("can[") : i+end])
		}
	} else if cm := nativeCanRE.FindStringSubmatch(tail); cm != nil {
		// Native unbracketed form: `... can Unsafe.X, Unsafe.Y` (like Elisa's `can`), ending
		// at the next `abi`/`for` clause or end of the header.
		fn.Effects = splitCSV(cm[1])
	}
	if pm := platformForRE.FindStringSubmatch(tail); pm != nil {
		fn.Platform = strings.TrimSpace(pm[1])
	}
	return fn, nil
}

func VerifyModule(module *Module) []Issue {
	if module == nil {
		return nil
	}
	var issues []Issue
	layouts := map[string]*Layout{}
	for i := range module.Layouts {
		l := &module.Layouts[i]
		if l.Name != "" {
			layouts[l.Name] = l
		}
	}
	seenExports := map[string]int{}
	for i := range module.Functions {
		fn := &module.Functions[i]
		if firstLine, ok := seenExports[fn.Name]; ok && fn.Name != "" {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-export", File: module.Path, Line: fn.Line, Message: fmt.Sprintf("EASM export %s duplicates earlier export on line %d", fn.Name, firstLine)})
		} else {
			seenExports[fn.Name] = fn.Line
		}
		if len(fn.Reference) > 0 || len(fn.Targets) > 0 {
			issues = append(issues, verifyLockstepRoutine(module.Path, module.Target, fn, layouts)...)
			continue
		}
		issues = append(issues, verifyFunction(module.Path, module.Target, fn, layouts)...)
	}
	return issues
}

// verifyLockstepRoutine handles a routine carrying a `reference` spec body and/or one or more
// `target … lockstep` implementations. It enforces the structural proof obligations that must hold
// before any runtime equivalence check is meaningful, then verifies the reference and every target
// body with the FULL machinery (clobbers, frame conditions, capabilities, …) as if each were an
// ordinary function body — so an optimized target gets no free pass. When
// ELISA_EASM_LOCKSTEP_ORACLE=1 is set, safe leaf targets are also fuzzed against the reference by a
// narrow observational oracle. Skips are reported explicitly; a skip is not a pass.
func verifyLockstepRoutine(path, target string, fn *Function, layouts map[string]*Layout) []Issue {
	var issues []Issue
	hasReference := len(fn.Reference) > 0
	for _, tb := range fn.Targets {
		name := strings.ToLower(strings.TrimSpace(tb.Lockstep))
		if name != "reference" {
			issues = append(issues, Issue{Severity: "error", Code: "lockstep-unknown-reference", File: path, Line: tb.Line, Message: fmt.Sprintf("target %s declares `lockstep %s`, but the only spec block a routine may reference is `reference`", tb.Arch, tb.Lockstep)})
		} else if !hasReference {
			issues = append(issues, Issue{Severity: "error", Code: "lockstep-missing-reference", File: path, Line: tb.Line, Message: fmt.Sprintf("target %s declares `lockstep reference`, but the routine has no `reference:` spec body to validate against", tb.Arch)})
		}
	}
	// Verify each body in isolation using the full per-instruction machinery.
	verifyBody := func(insts []Instruction) {
		sub := *fn
		sub.Instructions = insts
		sub.Reference = nil
		sub.Targets = nil
		issues = append(issues, verifyFunction(path, target, &sub, layouts)...)
	}
	if hasReference {
		verifyBody(fn.Reference)
	}
	for _, tb := range fn.Targets {
		verifyBody(tb.Instructions)
	}
	if os.Getenv("ELISA_EASM_LOCKSTEP_ORACLE") == "1" && !hasErrorIssue(issues) {
		issues = append(issues, verifyLockstepOracle(path, target, fn)...)
	}
	if os.Getenv("ELISA_EASM_LOCKSTEP_SYMBOLIC") == "1" && !hasErrorIssue(issues) {
		issues = append(issues, verifyLockstepSymbolic(path, target, fn)...)
	}
	return issues
}

// verifyPreservationParametricGate runs the register-polymorphic preservation check on an ordinary
// routine when ELISA_EASM_PRESERVE_PARAMETRIC=1. Called from the ordinary-function verify path.
func verifyPreservationParametricGate(path string, fn *Function, hasErr bool) []Issue {
	if os.Getenv("ELISA_EASM_PRESERVE_PARAMETRIC") != "1" || hasErr {
		return nil
	}
	return verifyPreservationParametric(path, fn)
}

// parseLayoutSize converts the optional `size N` capture of a `layout Name size N:` header into a
// declared byte size. An empty capture (no `size` clause) yields 0, which signals "infer bounds
// from the declared fields" (LayoutSize) — preserving the behavior of size-less layouts. The regex
// guarantees the capture is all digits, so a parse failure is impossible and treated as absent.
func parseLayoutSize(raw string) int64 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseFragmentHeader(path string, line int, text string) (Function, *Issue) {
	m := fragmentHeaderRE.FindStringSubmatch(text)
	if m == nil {
		return Function{}, &Issue{Severity: "error", Code: "invalid-fragment", File: path, Line: line, Message: "invalid fragment header"}
	}
	fr := Function{Name: m[1], Line: line, IsFragment: true}
	seen := map[string]bool{}
	for _, part := range splitCSV(m[2]) {
		if part == "" {
			continue
		}
		name := part
		typ := ""
		if pieces := strings.SplitN(part, ":", 2); len(pieces) == 2 {
			name = strings.TrimSpace(pieces[0])
			typ = strings.TrimSpace(pieces[1])
		}
		if seen[name] {
			return fr, &Issue{Severity: "error", Code: "duplicate-param", File: path, Line: line, Message: fmt.Sprintf("duplicate fragment parameter %s", name)}
		}
		seen[name] = true
		fr.Params = append(fr.Params, Param{Name: name, Type: typ})
	}
	return fr, nil
}

// expandCompositions resolves every `compose` directive by splicing the named fragment's
// body into the host function and materializing one concrete variant per subset of the
// composition flags. It is pure desugaring that runs BEFORE verifyFunction, so the verifier
// certifies the spliced instruction stream of each variant exactly as if it had been written
// inline — a fragment is never certified in isolation, and no unchecked sequence can be
// smuggled through composition.
func expandCompositions(module *Module) []Issue {
	if module == nil {
		return nil
	}
	var issues []Issue
	frags := map[string]*Function{}
	for i := range module.Fragments {
		f := &module.Fragments[i]
		if f.Name == "" {
			continue
		}
		if _, dup := frags[f.Name]; dup {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-fragment", File: module.Path, Line: f.Line, Message: fmt.Sprintf("EASM fragment %s already defined", f.Name)})
		}
		frags[f.Name] = f
		for _, inst := range f.Instructions {
			if inst.Op == "compose" {
				issues = append(issues, Issue{Severity: "error", Code: "nested-compose", File: module.Path, Line: inst.Line, Message: fmt.Sprintf("EASM fragment %s may not compose another fragment", f.Name)})
			}
		}
	}
	var expanded []Function
	for i := range module.Functions {
		fn := &module.Functions[i]
		if !functionComposes(fn) {
			expanded = append(expanded, *fn)
			continue
		}
		variants, vIssues := materializeComposition(module.Path, fn, frags)
		issues = append(issues, vIssues...)
		expanded = append(expanded, variants...)
	}
	module.Functions = expanded
	return issues
}

func functionComposes(fn *Function) bool {
	for _, inst := range fn.Instructions {
		if inst.Op == "compose" {
			return true
		}
	}
	return false
}

func materializeComposition(path string, fn *Function, frags map[string]*Function) ([]Function, []Issue) {
	var issues []Issue
	var flagOrder []string
	flagSeen := map[string]bool{}
	for _, inst := range fn.Instructions {
		if inst.Op != "compose" {
			continue
		}
		flag, name, args, ok := parseComposeDirective(inst.Text)
		if !ok {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-compose", File: path, Line: inst.Line, Message: fmt.Sprintf("invalid compose directive %q", inst.Text)})
			continue
		}
		frag, found := frags[name]
		if !found {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-fragment", File: path, Line: inst.Line, Message: fmt.Sprintf("compose references unknown fragment %s", name)})
			continue
		}
		if len(args) != len(frag.Params) {
			issues = append(issues, Issue{Severity: "error", Code: "fragment-arity", File: path, Line: inst.Line, Message: fmt.Sprintf("fragment %s expects %d argument(s), got %d", name, len(frag.Params), len(args))})
		}
		if flag != "" && !flagSeen[flag] {
			flagSeen[flag] = true
			flagOrder = append(flagOrder, flag)
		}
	}
	if len(issues) > 0 {
		return nil, issues
	}
	var variants []Function
	n := len(flagOrder)
	for mask := 0; mask < (1 << uint(n)); mask++ {
		active := map[string]bool{}
		var suffixParts []string
		for bit, flag := range flagOrder {
			if mask&(1<<uint(bit)) != 0 {
				active[flag] = true
				suffixParts = append(suffixParts, flag)
			}
		}
		variant := *fn
		variant.IsFragment = false
		variant.Requires = append([]string(nil), fn.Requires...)
		variant.Clobbers = append([]string(nil), fn.Clobbers...)
		variant.Preserves = append([]string(nil), fn.Preserves...)
		variant.Instructions = nil
		for _, inst := range fn.Instructions {
			if inst.Op != "compose" {
				variant.Instructions = append(variant.Instructions, inst)
				continue
			}
			flag, name, args, _ := parseComposeDirective(inst.Text)
			if flag != "" && !active[flag] {
				continue
			}
			frag := frags[name]
			subst := map[string]string{}
			for idx, p := range frag.Params {
				subst[p.Name] = args[idx]
			}
			for _, finst := range frag.Instructions {
				spliced := finst
				spliced.Text = substituteParams(finst.Text, subst)
				spliced.Line = inst.Line
				variant.Instructions = append(variant.Instructions, spliced)
			}
			variant.Requires = appendUnique(variant.Requires, frag.Requires)
			variant.Clobbers = appendUnique(variant.Clobbers, frag.Clobbers)
			variant.Preserves = appendUnique(variant.Preserves, frag.Preserves)
		}
		if len(suffixParts) > 0 {
			variant.Name = fn.Name + "__" + strings.Join(suffixParts, "_")
		}
		variant.Inputs = append([]string(nil), fn.Inputs...)
		variants = append(variants, variant)
	}
	pruneConditionalInputs(variants)
	return variants, issues
}

// pruneConditionalInputs drops an input *binding* from the variants that do not read it,
// but only when that input is read by some OTHER variant (i.e. it is conditional on a
// composition flag — like a guest-fs selector consumed only by the WithFs variant). An
// input read by NO variant is genuinely dead and is kept everywhere, so the verifier's
// input-register-unused check still fires on it. This mirrors the hand-written world, where
// the base and WithFs entry points have different signatures.
func pruneConditionalInputs(variants []Function) {
	readSets := make([]map[string]bool, len(variants))
	everUsed := map[string]bool{}
	for i := range variants {
		readSets[i] = canonicalReadSet(variants[i].Instructions)
		for reg := range readSets[i] {
			everUsed[reg] = true
		}
	}
	for i := range variants {
		var keptInputs []string
		droppedParams := map[string]bool{}
		for _, input := range variants[i].Inputs {
			canon := canonicalX86GPR(registerAfterEquals(input))
			if canon != "" && everUsed[canon] && !readSets[i][canon] {
				if name := bindingName(input); name != "" {
					droppedParams[name] = true
				}
				continue
			}
			keptInputs = append(keptInputs, input)
		}
		variants[i].Inputs = keptInputs
		if len(droppedParams) > 0 {
			// A conditional input's parameter only exists in variants that consume it, so the
			// base variant's signature is genuinely smaller — exactly like the hand-written
			// JumpMainEntry vs JumpMainEntryWithFs pair.
			var keptParams []Param
			for _, p := range variants[i].Params {
				if droppedParams[p.Name] {
					continue
				}
				keptParams = append(keptParams, p)
			}
			variants[i].Params = keptParams
		}
	}
}

func canonicalReadSet(insts []Instruction) map[string]bool {
	set := map[string]bool{}
	for _, inst := range insts {
		if inst.Pseudo {
			continue
		}
		for _, reg := range registersReadBy(inst.Text) {
			if canon := canonicalX86GPR(reg); canon != "" {
				set[canon] = true
			}
		}
	}
	return set
}

func parseComposeDirective(text string) (flag string, name string, args []string, ok bool) {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "compose"))
	head := s
	if open := strings.Index(s, "("); open >= 0 {
		closeIdx := strings.LastIndex(s, ")")
		if closeIdx < open {
			return "", "", nil, false
		}
		head = strings.TrimSpace(s[:open])
		for _, a := range strings.Split(s[open+1:closeIdx], ",") {
			if a = strings.TrimSpace(a); a != "" {
				args = append(args, a)
			}
		}
	}
	switch fields := strings.Fields(head); len(fields) {
	case 1:
		name = fields[0]
	case 2:
		flag, name = fields[0], fields[1]
	default:
		return "", "", nil, false
	}
	return flag, name, args, true
}

func substituteParams(text string, subst map[string]string) string {
	// Substitution is sequential, so the hole order must not depend on map order: longest names
	// first (a hole whose name contains another's is never clipped), ties by name.
	names := slices.Sorted(maps.Keys(subst))
	sort.SliceStable(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		arg := subst[name]
		// Legacy bracketed form (kept as an alias).
		text = strings.ReplaceAll(text, "<"+name+">", arg)
		// Native bare-name form (matches the template surface): replace a whole-word hole
		// reference that is not part of a %register or $immediate token.
		re := regexp.MustCompile(`(^|[^%$\w.])` + regexp.QuoteMeta(name) + `($|[^\w])`)
		repl := "${1}" + strings.ReplaceAll(arg, "$", "$$") + "${2}" // $$ = literal $ in replacement
		for {
			next := re.ReplaceAllString(text, repl)
			if next == text {
				break
			}
			text = next
		}
	}
	return text
}

func appendUnique(dst []string, add []string) []string {
	seen := map[string]bool{}
	for _, v := range dst {
		seen[strings.TrimSpace(v)] = true
	}
	for _, v := range add {
		if v = strings.TrimSpace(v); v != "" && !seen[v] {
			seen[v] = true
			dst = append(dst, v)
		}
	}
	return dst
}
