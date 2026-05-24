package easm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Module struct {
	Path      string
	Name      string
	Target    string
	Functions []Function
}

type Function struct {
	Name         string
	Params       []Param
	ReturnType   string
	ABI          string
	Effects      []string
	Inputs       []string
	Outputs      []string
	Clobbers     []string
	Preserves    []string
	Stack        []string
	Control      []string
	Requires     []string
	Instructions []Instruction
	Line         int
}

type Param struct {
	Name string
	Type string
}

type Instruction struct {
	Op   string
	Text string
	Line int
}

type Issue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Message  string `json:"message"`
}

type Report struct {
	TargetTriple string          `json:"targetTriple,omitempty"`
	Files        []string        `json:"files"`
	Modules      []ModuleSummary `json:"modules"`
	Issues       []Issue         `json:"issues,omitempty"`
}

type ModuleSummary struct {
	Path     string            `json:"path"`
	Name     string            `json:"name"`
	Target   string            `json:"target"`
	Exports  []FunctionSummary `json:"exports"`
	Requires []string          `json:"requires,omitempty"`
}

type FunctionSummary struct {
	Name       string   `json:"name"`
	ABI        string   `json:"abi,omitempty"`
	Params     []Param  `json:"params,omitempty"`
	ReturnType string   `json:"returnType,omitempty"`
	Control    []string `json:"control,omitempty"`
	Stack      []string `json:"stack,omitempty"`
}

var (
	exportHeaderRE = regexp.MustCompile(`^export\s+def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*->\s*([A-Za-z_][A-Za-z0-9_\[\]&?]*)\s*(.*):\s*$`)
	sectionRE      = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*:\s*(.*)$`)
	allowedOps     = map[string]bool{
		"mov": true, "movq": true, "lea": true, "push": true, "pushq": true, "pop": true, "popq": true,
		"movb": true, "movw": true, "movl": true, "movsx": true, "movsxd": true, "movzx": true,
		"add": true, "addq": true, "sub": true, "subq": true, "and": true, "andq": true,
		"cmp": true, "cmpq": true, "test": true, "testq": true, "inc": true, "incq": true, "dec": true, "decq": true,
		"xor": true, "xorq": true, "call": true, "callq": true, "jmp": true, "ret": true,
		"cpuid": true, "cld": true, "std": true,
		"lfence": true, "rdtsc": true, "pause": true, "yield": true, "mrs": true, "isb": true,
		"fldcw": true, "fnstcw": true, "stmxcsr": true, "ldmxcsr": true, "emms": true,
		"vzeroall": true, "trap": true,
	}
	capabilityByOp = map[string]string{
		"rdtsc": "x86_64.rdtsc", "pause": "x86_64.sse.pause", "yield": "aarch64.yield",
		"cpuid": "x86_64.cpuid",
		"mrs":   "aarch64.cntvct", "isb": "aarch64.cntvct", "fldcw": "x86_64.fpu_control",
		"fnstcw": "x86_64.fpu_control", "stmxcsr": "x86_64.fpu_control", "ldmxcsr": "x86_64.fpu_control",
		"emms": "x86_64.fpu_control", "vzeroall": "x86_64.simd_state", "trap": "debug.trap",
	}
)

func ParseFile(path string) (*Module, []Issue) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []Issue{{Severity: "error", Code: "read-failed", File: path, Message: err.Error()}}
	}
	return Parse(path, string(data))
}

func Parse(path string, src string) (*Module, []Issue) {
	module := &Module{Path: path, Target: "any"}
	var issues []Issue
	var current *Function
	var section string
	scanner := bufio.NewScanner(strings.NewReader(src))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := stripComment(strings.TrimSpace(raw))
		if line == "" {
			continue
		}
		if current == nil {
			switch {
			case strings.HasPrefix(line, "module "):
				module.Name = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			case strings.HasPrefix(line, "target "):
				module.Target = strings.TrimSpace(strings.TrimPrefix(line, "target "))
			case exportHeaderRE.MatchString(line):
				fn, issue := parseFunctionHeader(path, lineNo, line)
				if issue != nil {
					issues = append(issues, *issue)
					continue
				}
				module.Functions = append(module.Functions, fn)
				current = &module.Functions[len(module.Functions)-1]
				section = ""
			default:
				issues = append(issues, Issue{Severity: "error", Code: "unexpected-top-level", File: path, Line: lineNo, Message: "expected module, target, or export def"})
			}
			continue
		}
		if exportHeaderRE.MatchString(line) {
			fn, issue := parseFunctionHeader(path, lineNo, line)
			if issue != nil {
				issues = append(issues, *issue)
				continue
			}
			module.Functions = append(module.Functions, fn)
			current = &module.Functions[len(module.Functions)-1]
			section = ""
			continue
		}
		if matches := sectionRE.FindStringSubmatch(line); matches != nil && isSection(matches[1]) {
			section = strings.ToLower(matches[1])
			if rest := strings.TrimSpace(matches[2]); rest != "" {
				addSectionValue(current, section, rest, lineNo)
			}
			continue
		}
		if section == "" {
			issues = append(issues, Issue{Severity: "error", Code: "missing-section", File: path, Line: lineNo, Message: "function body line appears before a contract section"})
			continue
		}
		addSectionValue(current, section, lineWithIndent(raw), lineNo)
	}
	if err := scanner.Err(); err != nil {
		issues = append(issues, Issue{Severity: "error", Code: "scan-failed", File: path, Message: err.Error()})
	}
	if strings.TrimSpace(module.Name) == "" {
		module.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return module, append(issues, VerifyModule(module)...)
}

func parseFunctionHeader(path string, line int, text string) (Function, *Issue) {
	m := exportHeaderRE.FindStringSubmatch(text)
	if m == nil {
		return Function{}, &Issue{Severity: "error", Code: "invalid-export", File: path, Line: line, Message: "invalid export def header"}
	}
	fn := Function{Name: m[1], ReturnType: m[3], Line: line}
	for _, part := range splitCSV(m[2]) {
		if part == "" {
			continue
		}
		pieces := strings.SplitN(part, ":", 2)
		if len(pieces) != 2 {
			return fn, &Issue{Severity: "error", Code: "invalid-param", File: path, Line: line, Message: "expected parameter as name: type"}
		}
		fn.Params = append(fn.Params, Param{Name: strings.TrimSpace(pieces[0]), Type: strings.TrimSpace(pieces[1])})
	}
	tail := strings.TrimSpace(m[4])
	if strings.HasPrefix(tail, "abi ") {
		fields := strings.Fields(tail)
		if len(fields) >= 2 {
			fn.ABI = fields[1]
		}
	}
	if i := strings.Index(tail, "effects["); i >= 0 {
		end := strings.Index(tail[i:], "]")
		if end >= 0 {
			fn.Effects = splitCSV(tail[i+len("effects[") : i+end])
		}
	}
	return fn, nil
}

func VerifyModule(module *Module) []Issue {
	if module == nil {
		return nil
	}
	var issues []Issue
	for i := range module.Functions {
		fn := &module.Functions[i]
		issues = append(issues, verifyFunction(module.Path, module.Target, fn)...)
	}
	return issues
}

func verifyFunction(path string, target string, fn *Function) []Issue {
	var issues []Issue
	if fn.Name == "" {
		return append(issues, Issue{Severity: "error", Code: "missing-name", File: path, Line: fn.Line, Message: "EASM export is missing a symbol name"})
	}
	stackSet := setOf(fn.Stack)
	controlSet := setOf(fn.Control)
	clobberSet := setOf(fn.Clobbers)
	requireSet := setOf(fn.Requires)
	preserveSet := setOf(fn.Preserves)
	stackDelta := 0
	touchesStack := false
	mutatesStack := false
	usesCall := false
	usesJmp := false
	usesRet := false
	flagsLive := false
	wrotePartialReturn := false
	directionFlagSet := false
	for _, inst := range fn.Instructions {
		op := normalizeOp(inst.Op)
		if !allowedOps[op] && !isConditionalJump(op) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-instruction", File: path, Line: inst.Line, Message: fmt.Sprintf("unsupported EASM instruction %q", inst.Op)})
		}
		if cap := capabilityByOp[op]; cap != "" && !requireSet[cap] {
			issues = append(issues, Issue{Severity: "error", Code: "missing-capability", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q requires capability %s", inst.Op, cap)})
		}
		if strings.Contains(inst.Text, "rsp") || strings.Contains(inst.Text, "sp") || strings.HasPrefix(op, "push") || strings.HasPrefix(op, "pop") {
			touchesStack = true
		}
		if writesMemory(inst.Text) && !clobberSet["memory"] {
			issues = append(issues, Issue{Severity: "error", Code: "memory-write-without-clobber", File: path, Line: inst.Line, Message: "memory write requires memory clobber"})
		}
		if hasSuspiciousAbsoluteAddress(inst.Text) && !requireSet["fixed_address"] {
			issues = append(issues, Issue{Severity: "error", Code: "hard-coded-address", File: path, Line: inst.Line, Message: "hard-coded absolute address requires fixed_address capability"})
		}
		if writesPartialReturnRegister(inst.Text) {
			wrotePartialReturn = true
		}
		for _, reg := range implicitClobbers(op) {
			if !clobberSet[reg] {
				issues = append(issues, Issue{Severity: "error", Code: "implicit-clobber-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q implicitly clobbers %s", inst.Op, reg)})
			}
		}
		if usesHighByteRegister(inst.Text) && !requireSet["x86_64.legacy_high_byte"] {
			issues = append(issues, Issue{Severity: "error", Code: "high-byte-register", File: path, Line: inst.Line, Message: "high-byte registers require x86_64.legacy_high_byte because they interact poorly with modern x86-64 encodings"})
		}
		if instructionClobbersFlags(op) && !clobberSet["cc"] && !clobberSet["flags"] {
			issues = append(issues, Issue{Severity: "error", Code: "cc-clobber-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q changes condition codes but clobbers does not include cc or flags", inst.Op)})
		}
		switch {
		case strings.HasPrefix(op, "push"):
			mutatesStack = true
			stackDelta -= 8
		case strings.HasPrefix(op, "pop"):
			mutatesStack = true
			stackDelta += 8
		case op == "sub" || op == "subq":
			flagsLive = false
			if strings.Contains(inst.Text, "rsp") {
				mutatesStack = true
				stackDelta -= immediateValue(inst.Text)
			}
		case op == "add" || op == "addq":
			flagsLive = false
			if strings.Contains(inst.Text, "rsp") {
				mutatesStack = true
				stackDelta += immediateValue(inst.Text)
			}
		case op == "and" || op == "andq":
			flagsLive = false
			if strings.Contains(inst.Text, "rsp") {
				mutatesStack = true
				stackDelta = 0
			}
		case op == "mov" || op == "movq" || op == "lea":
			if writesStackPointer(inst.Text) {
				mutatesStack = true
			}
		case strings.HasPrefix(op, "call"):
			usesCall = true
			flagsLive = false
		case op == "jmp":
			usesJmp = true
		case op == "ret":
			usesRet = true
		case op == "std":
			directionFlagSet = true
		case op == "cld":
			directionFlagSet = false
		case op == "cmp" || op == "cmpq" || op == "test" || op == "testq":
			flagsLive = true
		case isConditionalJump(op):
			if !flagsLive {
				issues = append(issues, Issue{Severity: "error", Code: "stale-flags-branch", File: path, Line: inst.Line, Message: "conditional jump uses flags that are not known live from cmp/test"})
			}
			if isSignedConditionalJump(op) && !requireSet["compare.signed"] {
				issues = append(issues, Issue{Severity: "error", Code: "signed-branch-intent-missing", File: path, Line: inst.Line, Message: "signed conditional jump requires compare.signed intent"})
			}
			if isUnsignedConditionalJump(op) && !requireSet["compare.unsigned"] {
				issues = append(issues, Issue{Severity: "error", Code: "unsigned-branch-intent-missing", File: path, Line: inst.Line, Message: "unsigned conditional jump requires compare.unsigned intent"})
			}
		case instructionClobbersFlags(op):
			flagsLive = false
		}
	}
	for _, req := range fn.Requires {
		if !targetAllowsCapability(target, req) {
			issues = append(issues, Issue{Severity: "error", Code: "target-capability-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("target %s does not allow capability %s", defaultString(target, "any"), req)})
		}
	}
	if touchesStack && !stackSet["synthetic"] && !stackSet["switches"] && !stackSet["unchanged"] && !stackSet["noreturn"] {
		issues = append(issues, Issue{Severity: "error", Code: "stack-effect-undeclared", File: path, Line: fn.Line, Message: "function touches stack but stack contract does not declare unchanged, synthetic, switches, or noreturn"})
	}
	if mutatesStack && !clobberSet["memory"] {
		issues = append(issues, Issue{Severity: "error", Code: "stack-without-memory-clobber", File: path, Line: fn.Line, Message: "stack mutation requires memory clobber"})
	}
	if usesCall && !stackSet["aligned"] && !stackSet["switches"] && !stackSet["synthetic"] {
		issues = append(issues, Issue{Severity: "error", Code: "call-without-stack-contract", File: path, Line: fn.Line, Message: "call requires an aligned, synthetic, or switching stack contract"})
	}
	if wrotePartialReturn && returnsWideInteger(fn.ReturnType) {
		issues = append(issues, Issue{Severity: "error", Code: "partial-register-return", File: path, Line: fn.Line, Message: "wide integer return is written through a partial return register"})
	}
	if directionFlagSet && controlSet["returns"] {
		issues = append(issues, Issue{Severity: "error", Code: "direction-flag-not-restored", File: path, Line: fn.Line, Message: "returning function leaves the x86 direction flag set; use cld before returning"})
	}
	if controlSet["returns"] && stackSet["unchanged"] && stackDelta != 0 {
		issues = append(issues, Issue{Severity: "error", Code: "returning-stack-leak", File: path, Line: fn.Line, Message: fmt.Sprintf("returning function leaves symbolic stack delta %d", stackDelta)})
	}
	if stackSet["synthetic"] && usesCall && !usesJmp {
		issues = append(issues, Issue{Severity: "error", Code: "guest-entry-call-mangles-stack", File: path, Line: fn.Line, Message: "synthetic stack handoff must tail jump instead of call"})
	}
	if controlSet["noreturn"] && usesRet {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-can-return", File: path, Line: fn.Line, Message: "noreturn function contains ret"})
	}
	if controlSet["noreturn"] && !usesJmp && !hasOp(fn.Instructions, "trap") {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-missing-terminal", File: path, Line: fn.Line, Message: "noreturn function must end in jmp or trap"})
	}
	if controlSet["returns"] && usesJmp && !controlSet["tail_jumps"] {
		issues = append(issues, Issue{Severity: "error", Code: "returning-unqualified-jump", File: path, Line: fn.Line, Message: "returning function contains jmp without tail_jumps control contract"})
	}
	if controlSet["returns"] && !usesRet && fn.ReturnType == "void" {
		issues = append(issues, Issue{Severity: "error", Code: "returns-missing-ret", File: path, Line: fn.Line, Message: "returning void function must contain ret"})
	}
	for _, reg := range []string{"rbx", "rbp", "r12", "r13", "r14", "r15"} {
		if clobberSet[reg] && !preserveSet[reg] && !preserveSet["callee_saved"] {
			issues = append(issues, Issue{Severity: "error", Code: "callee-saved-not-preserved", File: path, Line: fn.Line, Message: fmt.Sprintf("callee-saved register %s is clobbered without preservation contract", reg)})
		}
	}
	_ = target
	return issues
}

func BuildReport(paths []string, targetTriple string) (*Report, []*Module) {
	report := &Report{TargetTriple: targetTriple}
	var modules []*Module
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
			summary.Exports = append(summary.Exports, FunctionSummary{Name: fn.Name, ABI: fn.ABI, Params: fn.Params, ReturnType: fn.ReturnType, Control: fn.Control, Stack: fn.Stack})
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

func isSection(s string) bool {
	switch strings.ToLower(s) {
	case "inputs", "outputs", "clobbers", "preserves", "stack", "control", "requires", "body":
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
	case "inputs":
		fn.Inputs = append(fn.Inputs, splitCSV(value)...)
	case "outputs":
		fn.Outputs = append(fn.Outputs, splitCSV(value)...)
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
	case "body":
		op := strings.Fields(value)
		if len(op) == 0 {
			return
		}
		fn.Instructions = append(fn.Instructions, Instruction{Op: op[0], Text: value, Line: line})
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func setOf(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		for _, field := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			if field != "" {
				out[strings.ToLower(strings.TrimSpace(field))] = true
			}
		}
	}
	return out
}

func normalizeOp(op string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(op), ";"))
}

func hasOp(instructions []Instruction, op string) bool {
	for _, inst := range instructions {
		if normalizeOp(inst.Op) == op {
			return true
		}
	}
	return false
}

func writesStackPointer(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	dst = strings.TrimPrefix(strings.ToLower(dst), "%")
	return dst == "rsp" || dst == "esp" || dst == "sp"
}

func targetAllowsCapability(target string, capability string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	capability = strings.ToLower(strings.TrimSpace(capability))
	if target == "" || target == "any" || capability == "" {
		return true
	}
	switch {
	case strings.HasPrefix(capability, "x86_64."):
		return strings.Contains(target, "x86_64") || strings.Contains(target, "amd64")
	case strings.HasPrefix(capability, "aarch64.") || strings.HasPrefix(capability, "arm64."):
		return strings.Contains(target, "aarch64") || strings.Contains(target, "arm64")
	default:
		return true
	}
}

func isConditionalJump(op string) bool {
	switch op {
	case "je", "jz", "jne", "jnz", "jl", "jle", "jg", "jge", "jb", "jbe", "ja", "jae":
		return true
	default:
		return false
	}
}

func isSignedConditionalJump(op string) bool {
	switch op {
	case "jl", "jle", "jg", "jge":
		return true
	default:
		return false
	}
}

func isUnsignedConditionalJump(op string) bool {
	switch op {
	case "jb", "jbe", "ja", "jae":
		return true
	default:
		return false
	}
}

func instructionClobbersFlags(op string) bool {
	switch op {
	case "add", "addq", "sub", "subq", "and", "andq", "xor", "xorq", "inc", "incq", "dec", "decq",
		"cmp", "cmpq", "test", "testq", "cld", "std":
		return true
	default:
		return false
	}
}

func implicitClobbers(op string) []string {
	switch op {
	case "rdtsc":
		return []string{"rax", "rdx"}
	case "cpuid":
		return []string{"rax", "rbx", "rcx", "rdx"}
	case "call", "callq":
		return []string{"cc"}
	default:
		return nil
	}
}

func usesHighByteRegister(text string) bool {
	lower := strings.ToLower(text)
	for _, reg := range []string{"%ah", "%bh", "%ch", "%dh"} {
		if strings.Contains(lower, reg) {
			return true
		}
	}
	return false
}

func writesMemory(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	return strings.Contains(dst, "(") && strings.Contains(dst, ")")
}

func hasSuspiciousAbsoluteAddress(text string) bool {
	for _, operand := range splitInstructionOperands(text) {
		operand = strings.TrimSpace(strings.ToLower(operand))
		if strings.HasPrefix(operand, "0x") && !strings.Contains(operand, "(") {
			return true
		}
		if strings.Contains(operand, "(0x") {
			return true
		}
	}
	return false
}

func writesPartialReturnRegister(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[len(parts)-1])), "%")
	switch dst {
	case "al", "ah", "ax", "eax":
		return true
	default:
		return false
	}
}

func returnsWideInteger(returnType string) bool {
	switch strings.TrimSpace(returnType) {
	case "i64", "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}

func splitInstructionOperands(text string) []string {
	fields := strings.Fields(text)
	if len(fields) <= 1 {
		return nil
	}
	operandText := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	return splitCSV(operandText)
}

func immediateValue(text string) int {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == '$' || r == '\t'
	})
	for _, field := range fields {
		var value int
		if _, err := fmt.Sscanf(field, "%d", &value); err == nil {
			return value
		}
	}
	return 0
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
