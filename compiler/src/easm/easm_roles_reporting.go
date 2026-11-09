package easm

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func verifyMachineRoleTypes(path string, fn *Function, requireSet map[string]bool, controlSet map[string]bool, layouts map[string]*Layout) []Issue {
	var issues []Issue
	paramTypes := map[string]string{}
	for _, param := range fn.Params {
		paramTypes[strings.ToLower(strings.TrimSpace(param.Name))] = strings.TrimSpace(param.Type)
	}
	if requireSet["x86_64.segment.write"] && !requireSet["x86_64.segment.selector.untyped"] {
		for _, param := range fn.Params {
			name := strings.ToLower(strings.TrimSpace(param.Name))
			if strings.Contains(name, "selector") && rawScalarType(param.Type) {
				issues = append(issues, Issue{Severity: "error", Code: "raw-segment-selector", File: path, Line: fn.Line, Message: fmt.Sprintf("segment-mutating EASM parameter %s uses raw %s; use GuestFsSelector or HostFsSelector, or require x86_64.segment.selector.untyped after a manual proof", param.Name, param.Type)})
			}
		}
	}
	if requireSet["control.indirect"] && !requireSet["control.target.untyped"] {
		for _, input := range fn.Inputs {
			name := strings.ToLower(bindingName(input))
			if name == "" || !looksLikeIndirectControlTargetName(name) {
				continue
			}
			if typ := paramTypes[name]; rawScalarType(typ) {
				issues = append(issues, Issue{Severity: "error", Code: "raw-indirect-control-target", File: path, Line: fn.Line, Message: fmt.Sprintf("indirect-control EASM parameter %s uses raw %s; use a role type such as GuestEntryPoint, GuestCallable, HostCallable, or ExitFunction, or require control.target.untyped after a manual proof", name, typ)})
			}
		}
	}
	if (controlSet["tail_jumps"] || controlSet["noreturn"]) && !requireSet["stack.owner.untyped"] {
		for _, param := range fn.Params {
			name := strings.ToLower(strings.TrimSpace(param.Name))
			if strings.Contains(name, "stack") && strings.Contains(name, "top") && rawScalarType(param.Type) {
				issues = append(issues, Issue{Severity: "error", Code: "raw-stack-handoff", File: path, Line: fn.Line, Message: fmt.Sprintf("stack-handoff EASM parameter %s uses raw %s; use GuestStackTop or require stack.owner.untyped after a manual proof", param.Name, param.Type)})
			}
		}
	}
	// A register dereferenced as a memory base carries an unstated "this is a valid pointer
	// into some memory" claim. Track simple register-to-register and lea-derived pointer
	// provenance from inputs so moving a raw uintptr into another register does not erase the
	// requirement to use a typed address-space carrier.
	if !requireSet["memory.base.untyped"] {
		regProvenance := map[string]memoryBaseProvenance{}
		for _, input := range fn.Inputs {
			if reg := registerAfterEquals(input); reg != "" {
				paramName := strings.ToLower(bindingName(input))
				typ := paramTypes[paramName]
				if rawScalarType(typ) || isAddressSpaceCarrierType(typ) || isEASMRoleType(typ) {
					regProvenance[canonicalX86GPR(reg)] = memoryBaseProvenance{Param: paramName, Type: typ, Raw: rawScalarType(typ)}
				}
			}
		}
		for _, inst := range fn.Instructions {
			if inst.Pseudo {
				continue
			}
			// lea computes an address without dereferencing memory; its memory operand is arithmetic,
			// not an access, so neither the raw-memory-base nor the layout-field check applies (a later
			// dereference of the computed pointer is where memory typing is enforced). Provenance still
			// flows through lea via updateMemoryBaseProvenance below.
			if !strings.HasPrefix(normalizeOp(inst.Op), "lea") {
				for _, operand := range splitInstructionOperands(inst.Text) {
					if !operandIsMemory(operand) {
						continue
					}
					base := memoryBaseRegister(operand)
					if base == "" || base == "rsp" || base == "rip" {
						continue
					}
					if provenance, ok := regProvenance[base]; ok && provenance.Raw {
						issues = append(issues, Issue{Severity: "error", Code: "raw-memory-base", File: path, Line: inst.Line, Message: fmt.Sprintf("memory base %%%s comes from EASM parameter %s of raw type %s; use an address-space carrier such as HostPtr[T] or GuestVAddr[T] that names the memory class, or require memory.base.untyped after a manual proof", base, provenance.Param, provenance.Type)})
					}
					if provenance, ok := regProvenance[base]; ok && !provenance.Raw {
						issues = append(issues, checkLayoutAccess(path, inst, operand, provenance, layouts)...)
					}
				}
			}
			updateMemoryBaseProvenance(regProvenance, inst.Text)
		}
	}
	issues = append(issues, checkFrameConditions(path, fn, paramTypes)...)
	// TAL item 4: existential handle types (docs/104). Additive — no-op unless the routine declares
	// a Handle[K] parameter or return type. See easm_handle.go.
	issues = append(issues, verifyHandleTypes(path, fn)...)
	return issues
}

// checkLayoutAccess type-checks one memory operand against the record layout of its carrier. When
// the base register's carrier is `HostPtr[L]`/`GuestVAddr[L]` for a declared layout L, the operand's
// constant displacement must land on a declared field, and the access width must match the field's
// width. Indexed (base,index,scale), symbolic-displacement, and extend/address-compute forms are
// skipped (not a single static field / suffix does not reflect memory width). Non-layout carriers
// (e.g. HostPtr[u32]) are unaffected.
func checkLayoutAccess(path string, inst Instruction, operand string, prov memoryBaseProvenance, layouts map[string]*Layout) []Issue {
	elem := layoutElemName(prov.Type)
	if elem == "" {
		return nil
	}
	layout, ok := layouts[elem]
	if !ok {
		return nil
	}
	if open := strings.Index(operand, "("); open >= 0 {
		if rel := strings.Index(operand[open:], ")"); rel >= 0 && strings.Contains(operand[open:open+rel], ",") {
			return nil
		}
	}
	disp, ok := memoryDisplacement(operand)
	if !ok {
		return nil
	}
	field, ok := layoutFieldAt(layout, disp)
	if !ok {
		return []Issue{{Severity: "error", Code: "layout-unknown-field", File: path, Line: inst.Line, Message: fmt.Sprintf("offset %d into %s of layout %s is not a declared field; add the field or correct the access", disp, prov.Param, layout.Name)}}
	}
	op := normalizeOp(inst.Op)
	if strings.HasPrefix(op, "movs") || strings.HasPrefix(op, "movz") || strings.HasPrefix(op, "lea") {
		return nil
	}
	if w, ok := accessWidthBytes(op); ok && field.Width != 0 && w != field.Width {
		return []Issue{{Severity: "error", Code: "layout-field-width-mismatch", File: path, Line: inst.Line, Message: fmt.Sprintf("%d-byte access at offset %d hits field %s.%s declared %s (%d bytes); widths must match", w, disp, layout.Name, field.Name, field.Type, field.Width)}}
	}
	return nil
}

// checkFrameConditions enforces precise `changes`/`reads` frame contracts. When a routine names
// the buffers it may write (`changes`) or read (`reads`), every memory access in the body must go
// through one of those named carrier parameters. This upgrades the coarse `clobbers: memory`
// discipline into a per-pointer guarantee: a stray write through any other carrier is rejected.
// Opt-in -- a routine that declares neither clause is governed only by the coarse memory clobbers,
// exactly as before. x86 instructions carry at most one memory operand, so attribution is exact.
func checkFrameConditions(path string, fn *Function, paramTypes map[string]string) []Issue {
	changes := lowerStringSet(fn.Changes)
	reads := lowerStringSet(fn.Reads)
	if len(changes) == 0 && len(reads) == 0 {
		return nil
	}
	var issues []Issue
	// A frame clause may only name actual parameters; a typo would otherwise silently widen the
	// permitted footprint.
	for _, decl := range []struct {
		clause string
		names  []string
	}{{"changes", fn.Changes}, {"reads", fn.Reads}} {
		for _, name := range decl.names {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, ok := paramTypes[key]; !ok {
				issues = append(issues, Issue{Severity: "error", Code: "frame-unknown-carrier", File: path, Line: fn.Line, Message: fmt.Sprintf("%s names %q, which is not a parameter of this routine", decl.clause, name)})
			}
		}
	}
	// Trace every memory base register back to the carrier parameter it came from, following
	// register-to-register moves, then require the access to be authorised by the frame.
	regProvenance := map[string]memoryBaseProvenance{}
	for _, input := range fn.Inputs {
		if reg := registerAfterEquals(input); reg != "" {
			paramName := strings.ToLower(bindingName(input))
			regProvenance[canonicalX86GPR(reg)] = memoryBaseProvenance{Param: paramName, Type: paramTypes[paramName]}
		}
	}
	for _, inst := range fn.Instructions {
		if inst.Pseudo {
			updateMemoryBaseProvenance(regProvenance, inst.Text)
			continue
		}
		writes := writesMemory(inst.Text)
		readsMem := readsMemory(inst.Text)
		if writes || readsMem {
			for _, operand := range splitInstructionOperands(inst.Text) {
				if !operandIsMemory(operand) {
					continue
				}
				base := memoryBaseRegister(operand)
				if base == "" || base == "rsp" || base == "rip" {
					continue
				}
				prov, ok := regProvenance[base]
				if !ok || prov.Param == "" {
					// Base is not traceable to a named carrier (e.g. a freshly computed scratch
					// pointer); the coarse memory clobber discipline still governs it.
					continue
				}
				if writes && !changes[prov.Param] {
					issues = append(issues, Issue{Severity: "error", Code: "frame-write-outside-changes", File: path, Line: inst.Line, Message: fmt.Sprintf("writes memory through %s but `changes` does not list it; add `changes: %s` or route the write through a declared buffer", prov.Param, prov.Param)})
				}
				if readsMem && !writes && !changes[prov.Param] && !reads[prov.Param] {
					issues = append(issues, Issue{Severity: "error", Code: "frame-read-outside-reads", File: path, Line: inst.Line, Message: fmt.Sprintf("reads memory through %s but neither `reads` nor `changes` lists it; add `reads: %s`", prov.Param, prov.Param)})
				}
			}
		}
		updateMemoryBaseProvenance(regProvenance, inst.Text)
	}
	return issues
}

func lowerStringSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, v := range values {
		if key := strings.ToLower(strings.TrimSpace(v)); key != "" {
			set[key] = true
		}
	}
	return set
}

type memoryBaseProvenance struct {
	Param string
	Type  string
	Raw   bool
}

func updateMemoryBaseProvenance(regProvenance map[string]memoryBaseProvenance, text string) {
	writtenRegs := writtenRegisters(text)
	if len(writtenRegs) == 0 {
		return
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		for _, reg := range writtenRegs {
			delete(regProvenance, canonicalX86GPR(reg))
		}
		return
	}
	op := normalizeOp(fields[0])
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		for _, reg := range writtenRegs {
			delete(regProvenance, canonicalX86GPR(reg))
		}
		return
	}
	if op == "xchg" || op == "xchgl" || op == "xchgq" {
		for _, reg := range writtenRegs {
			delete(regProvenance, canonicalX86GPR(reg))
		}
		return
	}
	written := canonicalX86GPR(writtenRegs[0])
	if written == "" || !isX86GPR(written) {
		return
	}
	switch op {
	case "mov", "movq", "movl", "movw", "movb":
		if src := registerOperand(parts[0]); src != "" {
			if provenance, ok := regProvenance[src]; ok {
				regProvenance[written] = provenance
				return
			}
		}
	case "lea", "leaq", "leal":
		if base := memoryBaseRegister(parts[0]); base != "" {
			if provenance, ok := regProvenance[base]; ok {
				regProvenance[written] = provenance
				return
			}
		}
	case "add", "addq", "sub", "subq", "inc", "incq", "dec", "decq":
		if provenance, ok := regProvenance[written]; ok {
			regProvenance[written] = provenance
			return
		}
	}
	delete(regProvenance, written)
}

func registerOperand(operand string) string {
	reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(operand)), "%")
	reg = canonicalX86GPR(reg)
	if isX86GPR(reg) {
		return reg
	}
	return ""
}

func rawScalarType(name string) bool {
	switch strings.TrimSpace(name) {
	case "u16", "u32", "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}

func looksLikeIndirectControlTargetName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "entry", "target", "callee", "callable", "fn", "func", "function", "code", "pc":
		return true
	default:
		return strings.HasSuffix(name, "_entry") || strings.HasSuffix(name, "_target") || strings.HasSuffix(name, "_pc")
	}
}

func BuildReport(paths []string, targetTriple string) (*Report, []*Module) {
	report := &Report{TargetTriple: targetTriple}
	var modules []*Module
	seenExports := map[string]string{}
	for _, path := range paths {
		module, issues := ParseFile(path)
		report.Files = append(report.Files, path)
		report.Issues = append(report.Issues, issues...)
		if module == nil {
			continue
		}
		modules = append(modules, module)
		summary := ModuleSummary{Path: module.Path, Name: module.Name, Target: module.Target}
		reqs := map[string]bool{}
		for _, fn := range module.Functions {
			labels := make([]string, 0, len(fn.Labels))
			for _, label := range fn.Labels {
				labels = append(labels, formatLabelContract(label))
			}
			if previous, ok := seenExports[fn.Name]; ok && fn.Name != "" {
				report.Issues = append(report.Issues, Issue{Severity: "error", Code: "duplicate-export", File: module.Path, Line: fn.Line, Message: fmt.Sprintf("EASM export %s duplicates export from %s", fn.Name, previous)})
			} else {
				seenExports[fn.Name] = module.Path
			}
			summary.Exports = append(summary.Exports, FunctionSummary{Name: fn.Name, ABI: fn.ABI, Params: fn.Params, ReturnType: fn.ReturnType, Facts: fn.Facts, Labels: labels, Control: fn.Control, Stack: fn.Stack, DerivedEffects: DerivedEffects(&fn)})
			for _, req := range fn.Requires {
				reqs[req] = true
			}
		}
		for req := range reqs {
			summary.Requires = append(summary.Requires, req)
		}
		sort.Strings(summary.Requires)
		report.Modules = append(report.Modules, summary)
	}
	return report, modules
}

func FormatReport(report *Report, jsonOutput bool) (string, error) {
	if report == nil {
		return "", nil
	}
	if jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data) + "\n", nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Target triple: %s\n", defaultString(report.TargetTriple, "<host-default>"))
	fmt.Fprintf(&out, "EASM files (%d):\n", len(report.Files))
	if len(report.Files) == 0 {
		out.WriteString("  - <none>\n")
	}
	for _, file := range report.Files {
		fmt.Fprintf(&out, "  - %s\n", file)
	}
	for _, module := range report.Modules {
		fmt.Fprintf(&out, "Module %s target=%s\n", module.Name, module.Target)
		for _, exported := range module.Exports {
			fmt.Fprintf(&out, "  export %s abi=%s control=%s stack=%s\n", exported.Name, defaultString(exported.ABI, "c"), strings.Join(exported.Control, ","), strings.Join(exported.Stack, ","))
			for _, fact := range exported.Facts {
				fmt.Fprintf(&out, "    fact %s\n", fact)
			}
			for _, label := range exported.Labels {
				fmt.Fprintf(&out, "    label %s\n", label)
			}
		}
	}
	if len(report.Issues) != 0 {
		out.WriteString("Issues:\n")
		for _, issue := range report.Issues {
			location := issue.File
			if issue.Line > 0 {
				location = fmt.Sprintf("%s:%d", issue.File, issue.Line)
			}
			fmt.Fprintf(&out, "  [%s] %s %s: %s\n", issue.Severity, issue.Code, location, issue.Message)
		}
	}
	return out.String(), nil
}

func formatLabelContract(label LabelContract) string {
	if len(label.Preconditions) == 0 {
		return label.Name
	}
	return label.Name + ": " + strings.Join(label.Preconditions, ", ")
}

func HasErrors(report *Report) bool {
	if report == nil {
		return false
	}
	for _, issue := range report.Issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

func stripComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		return strings.TrimSpace(line[:i])
	}
	return line
}

func lineWithIndent(line string) string { return stripComment(strings.TrimSpace(line)) }

// inlineContract recognises the native single-line contract form (`requires <values>`,
// `clobbers <values>`, `can <effects>`, …) so a function body needs no `body:` marker and no
// `section:` soup. Returns the lowercased keyword, the values after it, and whether it matched.
func inlineContract(line string) (string, string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	kw := strings.ToLower(fields[0])
	if kw == "body" || (!isSection(kw) && kw != "can") {
		return "", "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
	return kw, rest, true
}

func isSection(s string) bool {
	switch strings.ToLower(s) {
	case "facts", "inputs", "outputs", "labels", "clobbers", "preserves", "stack", "control", "requires", "changes", "reads", "body":
		return true
	default:
		return false
	}
}

func addSectionValue(fn *Function, section string, value string, line int) {
	if fn == nil || value == "" {
		return
	}
	switch section {
	case "facts":
		fn.Facts = append(fn.Facts, splitCSV(value)...)
	case "inputs":
		fn.Inputs = append(fn.Inputs, splitCSV(value)...)
	case "outputs":
		fn.Outputs = append(fn.Outputs, splitCSV(value)...)
	case "labels":
		fn.Labels = append(fn.Labels, parseLabelContract(value, line))
	case "clobbers":
		fn.Clobbers = append(fn.Clobbers, splitCSV(value)...)
	case "preserves":
		fn.Preserves = append(fn.Preserves, splitCSV(value)...)
	case "stack":
		fn.Stack = append(fn.Stack, splitCSV(value)...)
	case "control":
		fn.Control = append(fn.Control, splitCSV(value)...)
	case "requires":
		fn.Requires = append(fn.Requires, splitCSV(value)...)
	case "changes":
		fn.Changes = append(fn.Changes, splitCSV(value)...)
	case "reads":
		fn.Reads = append(fn.Reads, splitCSV(value)...)
	case "body":
		if inst, ok := parseBodyInstruction(value, line); ok {
			fn.Instructions = append(fn.Instructions, inst)
		}
	}
}

// EmittedBody returns the instruction stream that should be lowered to machine code for fn: the
// implementation selected from its `target … lockstep` blocks when present, otherwise the routine's
// primary body (falling back to the `reference` spec if the primary body is empty). Routines with no
// target blocks are returned byte-for-byte unchanged. Multi-target selection by build triple is a
// follow-on; a single-arch build carries one target block, so the first is used.
func EmittedBody(fn *Function) []Instruction {
	if fn == nil {
		return nil
	}
	if len(fn.Targets) > 0 {
		return fn.Targets[0].Instructions
	}
	if len(fn.Instructions) == 0 && len(fn.Reference) > 0 {
		return fn.Reference
	}
	return fn.Instructions
}

// addBodyOrSink routes a body line into the active `reference:`/`target` sub-block when one is
// open, and into the function's primary body otherwise.
func addBodyOrSink(fn *Function, sink *[]Instruction, value string, line int) {
	if sink == nil {
		addSectionValue(fn, "body", value, line)
		return
	}
	if inst, ok := parseBodyInstruction(value, line); ok {
		*sink = append(*sink, inst)
	}
}

// parseBodyInstruction turns one body line into an Instruction, shared by function bodies and
// template bodies so both accept the identical instruction surface.
func parseBodyInstruction(value string, line int) (Instruction, bool) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "compose ") {
		return Instruction{Op: "compose", Text: strings.TrimSpace(value), Line: line, Pseudo: true}, true
	}
	if state, ok := parseStateAssertion(value); ok {
		return Instruction{Op: "state", Text: state, Line: line, Pseudo: true}, true
	}
	if label, ok := parseBodyLabel(value); ok {
		return Instruction{Op: "label", Text: label + ":", Line: line, Label: label}, true
	}
	op := strings.Fields(value)
	if len(op) == 0 {
		return Instruction{}, false
	}
	return Instruction{Op: op[0], Text: value, Line: line}, true
}

func parseLabelContract(value string, line int) LabelContract {
	name, rest, ok := strings.Cut(value, ":")
	if !ok {
		return LabelContract{Name: strings.TrimSpace(value), Line: line}
	}
	return LabelContract{Name: strings.TrimSpace(name), Preconditions: splitCSV(rest), Line: line}
}

func parseStateAssertion(value string) (string, bool) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, prefix := range []string{"state ", "fact ", "assume "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(value[len(prefix):]), true
		}
	}
	return "", false
}

func parseBodyLabel(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasSuffix(value, ":") || strings.Contains(value, " ") || strings.Contains(value, "\t") {
		return "", false
	}
	label := strings.TrimSuffix(value, ":")
	if !isIdentifierLike(label) {
		return "", false
	}
	return label, true
}
