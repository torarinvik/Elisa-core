package easm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
		"xchg": true, "xchgl": true, "xchgq": true,
		"add": true, "addq": true, "sub": true, "subq": true, "and": true, "andq": true,
		"cmp": true, "cmpq": true, "test": true, "testq": true, "inc": true, "incq": true, "dec": true, "decq": true,
		"xor": true, "xorq": true, "call": true, "callq": true, "jmp": true, "ret": true,
		"cpuid": true, "cld": true, "std": true,
		"lfence": true, "rdtsc": true, "pause": true, "yield": true, "mrs": true, "isb": true,
		"fldcw": true, "fnstcw": true, "stmxcsr": true, "ldmxcsr": true, "emms": true,
		"vzeroall": true, "trap": true,
	}
	capabilityByOp = map[string]string{
		"rdtsc": "x86_64.rdtsc", "lfence": "x86_64.sse.lfence", "pause": "x86_64.sse.pause", "yield": "aarch64.yield",
		"cpuid": "x86_64.cpuid",
		"xchg":  "x86_64.atomic.rmw", "xchgl": "x86_64.atomic.rmw", "xchgq": "x86_64.atomic.rmw",
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
	seenExports := map[string]int{}
	for i := range module.Functions {
		fn := &module.Functions[i]
		if firstLine, ok := seenExports[fn.Name]; ok && fn.Name != "" {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-export", File: module.Path, Line: fn.Line, Message: fmt.Sprintf("EASM export %s duplicates earlier export on line %d", fn.Name, firstLine)})
		} else {
			seenExports[fn.Name] = fn.Line
		}
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
	outputSet := outputRegisterSet(fn.Outputs)
	returnsVoid := strings.TrimSpace(fn.ReturnType) == "void"
	stackDelta := 0
	maxEntryStackPopDelta := 0
	stackMod16 := 8
	stackMod16Known := strings.Contains(strings.ToLower(target), "x86_64") || strings.Contains(strings.ToLower(target), "amd64")
	maxStackAllocation := 0
	touchesStack := false
	mutatesStack := false
	usesCall := false
	usesJmp := false
	usesRet := false
	writesSP := false
	mayFault := false
	flagsLive := false
	wrotePartialReturn := false
	directionFlagSet := false
	rdtscSeen := false
	lfenceBeforeRDTSC := false
	lfenceAfterRDTSC := false
	lfenceSeen := false
	clobberedByCall := map[string]int{}
	for _, inst := range fn.Instructions {
		op := normalizeOp(inst.Op)
		if !allowedOps[op] && !isConditionalJump(op) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-instruction", File: path, Line: inst.Line, Message: fmt.Sprintf("unsupported EASM instruction %q", inst.Op)})
		}
		if cap := capabilityByOp[op]; cap != "" && !requireSet[cap] {
			issues = append(issues, Issue{Severity: "error", Code: "missing-capability", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q requires capability %s", inst.Op, cap)})
		}
		if usesStackRegister(inst.Text) || strings.HasPrefix(op, "push") || strings.HasPrefix(op, "pop") {
			touchesStack = true
		}
		if writesMemory(inst.Text) && !clobberSet["memory"] {
			issues = append(issues, Issue{Severity: "error", Code: "memory-write-without-clobber", File: path, Line: inst.Line, Message: "memory write requires memory clobber"})
		}
		if hasMemoryOperand(inst.Text) || strings.HasPrefix(op, "call") || op == "trap" {
			mayFault = true
		}
		if hasAmbiguousOperandSize(op, inst.Text) && !requireSet["operand_size.inferred"] {
			issues = append(issues, Issue{Severity: "error", Code: "ambiguous-operand-size", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q must use an explicit size suffix or require operand_size.inferred", inst.Op)})
		}
		if immediateOverflowsInstruction(op, inst.Text) && !requireSet["immediate.truncation"] {
			issues = append(issues, Issue{Severity: "error", Code: "immediate-truncation", File: path, Line: inst.Line, Message: "immediate does not fit the instruction width without truncation/sign-extension intent"})
		}
		if hasSuspiciousAbsoluteAddress(inst.Text) && !requireSet["fixed_address"] {
			issues = append(issues, Issue{Severity: "error", Code: "hard-coded-address", File: path, Line: inst.Line, Message: "hard-coded absolute address requires fixed_address capability"})
		}
		if usesSymbolAddress(inst.Text) && !requireSet["relocation.symbol"] && !requireSet["pic"] {
			issues = append(issues, Issue{Severity: "error", Code: "symbol-relocation-intent-missing", File: path, Line: inst.Line, Message: "symbol address/value use requires relocation.symbol or pic intent"})
		}
		for seg := range segmentOverridesUsed(inst.Text) {
			if !hasSegmentCapability(requireSet, seg) {
				issues = append(issues, Issue{Severity: "error", Code: "segment-access-intent-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("%s segment access requires x86_64.segment.%s or x86_64.segment", seg, seg)})
			}
		}
		for seg := range segmentRegistersUsed(inst.Text) {
			if !hasSegmentCapability(requireSet, seg) {
				issues = append(issues, Issue{Severity: "error", Code: "segment-register-intent-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("%s segment register use requires x86_64.segment.%s or x86_64.segment", seg, seg)})
			}
		}
		if writesSegmentRegister(inst.Text) && !requireSet["x86_64.segment.write"] {
			issues = append(issues, Issue{Severity: "error", Code: "segment-register-write-intent-missing", File: path, Line: inst.Line, Message: "writing fs/gs requires x86_64.segment.write intent"})
		}
		if writesSegmentRegister(inst.Text) && !clobberSet["memory"] {
			issues = append(issues, Issue{Severity: "error", Code: "segment-register-write-without-memory-clobber", File: path, Line: inst.Line, Message: "writing fs/gs changes TLS addressing and requires memory clobber"})
		}
		if written := writtenRegister(inst.Text); written != "" {
			canonical := canonicalX86GPR(written)
			if isX86GPR(canonical) && !clobberSet[canonical] && !outputSet[canonical] {
				issues = append(issues, Issue{Severity: "error", Code: "register-write-without-clobber", File: path, Line: inst.Line, Message: fmt.Sprintf("register %s is written but not declared as a clobber or output", canonical)})
			}
		}
		if isIndirectControlTransfer(op, inst.Text) && !requireSet["control.indirect"] {
			issues = append(issues, Issue{Severity: "error", Code: "indirect-control-intent-missing", File: path, Line: inst.Line, Message: "indirect call/jmp requires control.indirect intent"})
		}
		if isDirectSymbolControlTransfer(op, inst.Text) && !requireSet["control.direct"] && !requireSet["relocation.symbol"] {
			issues = append(issues, Issue{Severity: "error", Code: "direct-control-intent-missing", File: path, Line: inst.Line, Message: "direct symbolic call/jmp requires control.direct or relocation.symbol intent"})
		}
		if writesPartialReturnRegister(inst.Text) {
			wrotePartialReturn = true
		}
		if usesReservedRegister(target, inst.Text, requireSet) {
			issues = append(issues, Issue{Severity: "error", Code: "reserved-register-use", File: path, Line: inst.Line, Message: "target-reserved register requires an explicit platform capability"})
		}
		for _, reg := range implicitClobbers(op) {
			if !clobberSet[reg] {
				issues = append(issues, Issue{Severity: "error", Code: "implicit-clobber-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q implicitly clobbers %s", inst.Op, reg)})
			}
		}
		if usesHighByteRegister(inst.Text) && !requireSet["x86_64.legacy_high_byte"] {
			issues = append(issues, Issue{Severity: "error", Code: "high-byte-register", File: path, Line: inst.Line, Message: "high-byte registers require x86_64.legacy_high_byte because they interact poorly with modern x86-64 encodings"})
		}
		if len(clobberedByCall) != 0 && !requireSet["call.caller_saved_liveness.unchecked"] {
			for _, reg := range callerSavedGPRs() {
				if callLine, ok := clobberedByCall[reg]; ok && instructionReadsRegisterBeforeWriting(inst.Text, reg) {
					issues = append(issues, Issue{Severity: "error", Code: "caller-saved-use-after-call", File: path, Line: inst.Line, Message: fmt.Sprintf("register %s is read after call on line %d; caller-saved registers must be reloaded or explicitly unchecked", reg, callLine)})
				}
			}
		}
		if instructionClobbersFlags(op) && !clobberSet["cc"] && !clobberSet["flags"] {
			issues = append(issues, Issue{Severity: "error", Code: "cc-clobber-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q changes condition codes but clobbers does not include cc or flags", inst.Op)})
		}
		switch {
		case strings.HasPrefix(op, "push"):
			mutatesStack = true
			stackDelta -= 8
			if stackMod16Known {
				stackMod16 = mod16(stackMod16 - 8)
			}
		case strings.HasPrefix(op, "pop"):
			mutatesStack = true
			stackDelta += 8
			if stackDelta > maxEntryStackPopDelta {
				maxEntryStackPopDelta = stackDelta
			}
			if stackMod16Known {
				stackMod16 = mod16(stackMod16 + 8)
			}
		case op == "sub" || op == "subq":
			flagsLive = false
			if usesStackRegister(inst.Text) {
				mutatesStack = true
				amount := immediateValue(inst.Text)
				stackDelta -= amount
				if stackMod16Known {
					stackMod16 = mod16(stackMod16 - amount)
				}
				if amount > maxStackAllocation {
					maxStackAllocation = amount
				}
			}
		case op == "add" || op == "addq":
			flagsLive = false
			if usesStackRegister(inst.Text) {
				mutatesStack = true
				amount := immediateValue(inst.Text)
				stackDelta += amount
				if stackDelta > maxEntryStackPopDelta {
					maxEntryStackPopDelta = stackDelta
				}
				if stackMod16Known {
					stackMod16 = mod16(stackMod16 + amount)
				}
			}
		case op == "and" || op == "andq":
			flagsLive = false
			if usesStackRegister(inst.Text) {
				mutatesStack = true
				stackDelta = 0
				if stackAndAlignsTo16(inst.Text) {
					stackMod16 = 0
					stackMod16Known = true
				} else {
					stackMod16Known = false
				}
			}
		case op == "mov" || op == "movq" || op == "lea":
			if writesStackPointer(inst.Text) {
				mutatesStack = true
				writesSP = true
				stackMod16Known = false
			}
		case strings.HasPrefix(op, "call"):
			if stackMod16Known && stackMod16 != 0 && !requireSet["stack.call_alignment.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "call-stack-misaligned", File: path, Line: inst.Line, Message: fmt.Sprintf("call executes with symbolic rsp mod 16 = %d; SysV callers must align rsp to 16 before call", stackMod16)})
			}
			if !stackMod16Known && !requireSet["stack.call_alignment.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "call-stack-alignment-unknown", File: path, Line: inst.Line, Message: "call executes after an untracked stack-pointer write; require stack.call_alignment.unchecked only after manual ABI proof"})
			}
			usesCall = true
			flagsLive = false
			for _, reg := range callerSavedGPRs() {
				clobberedByCall[reg] = inst.Line
			}
		case op == "jmp":
			usesJmp = true
		case op == "ret":
			usesRet = true
		case op == "std":
			directionFlagSet = true
		case op == "cld":
			directionFlagSet = false
		case op == "lfence":
			if rdtscSeen {
				lfenceAfterRDTSC = true
			} else {
				lfenceSeen = true
			}
		case op == "rdtsc":
			rdtscSeen = true
			lfenceBeforeRDTSC = lfenceSeen
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
		if written := writtenRegister(inst.Text); written != "" {
			delete(clobberedByCall, canonicalX86GPR(written))
		}
	}
	issues = append(issues, verifyABI(path, fn)...)
	issues = append(issues, verifyContractTokens(path, fn)...)
	issues = append(issues, verifySignatureTypes(path, fn)...)
	issues = append(issues, verifyBindings(path, target, fn)...)
	issues = append(issues, verifyRegisterLists(path, target, fn)...)
	issues = append(issues, verifyDuplicateContractAtoms(path, fn)...)
	if len(fn.Instructions) == 0 {
		issues = append(issues, Issue{Severity: "error", Code: "missing-body", File: path, Line: fn.Line, Message: "EASM export must contain a body"})
	}
	if len(fn.Stack) == 0 {
		issues = append(issues, Issue{Severity: "error", Code: "missing-stack-contract", File: path, Line: fn.Line, Message: "EASM export must declare stack behavior"})
	}
	if len(fn.Control) == 0 {
		issues = append(issues, Issue{Severity: "error", Code: "missing-control-contract", File: path, Line: fn.Line, Message: "EASM export must declare control behavior"})
	}
	if controlSet["returns"] && controlSet["noreturn"] {
		issues = append(issues, Issue{Severity: "error", Code: "conflicting-control-contract", File: path, Line: fn.Line, Message: "EASM export cannot declare both returns and noreturn"})
	}
	if controlSet["tail_jumps"] && !usesJmp {
		issues = append(issues, Issue{Severity: "error", Code: "tail-jumps-without-jump", File: path, Line: fn.Line, Message: "tail_jumps control contract requires a jmp instruction"})
	}
	if controlSet["may_fault"] && !mayFault {
		issues = append(issues, Issue{Severity: "error", Code: "may-fault-without-faulting-op", File: path, Line: fn.Line, Message: "may_fault control contract requires memory, call, or trap behavior"})
	}
	if !returnsVoid && !hasReturnOutput(fn.Outputs) {
		issues = append(issues, Issue{Severity: "error", Code: "missing-return-output", File: path, Line: fn.Line, Message: "non-void EASM export must declare outputs: ret = <register>"})
	}
	if returnsVoid && hasReturnOutput(fn.Outputs) {
		issues = append(issues, Issue{Severity: "error", Code: "void-return-output", File: path, Line: fn.Line, Message: "void EASM export cannot declare a ret output"})
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
	if maxStackAllocation > 4096 && !stackSet["probed"] {
		issues = append(issues, Issue{Severity: "error", Code: "large-stack-adjust-without-probe", File: path, Line: fn.Line, Message: "large stack allocation requires stack: probed"})
	}
	if wrotePartialReturn && returnsWideInteger(fn.ReturnType) {
		issues = append(issues, Issue{Severity: "error", Code: "partial-register-return", File: path, Line: fn.Line, Message: "wide integer return is written through a partial return register"})
	}
	if directionFlagSet && controlSet["returns"] {
		issues = append(issues, Issue{Severity: "error", Code: "direction-flag-not-restored", File: path, Line: fn.Line, Message: "returning function leaves the x86 direction flag set; use cld before returning"})
	}
	if rdtscSeen && (!lfenceBeforeRDTSC || !lfenceAfterRDTSC) && !requireSet["x86_64.rdtsc.unordered"] {
		issues = append(issues, Issue{Severity: "error", Code: "rdtsc-without-fence", File: path, Line: fn.Line, Message: "rdtsc requires lfence before and after, or explicit x86_64.rdtsc.unordered"})
	}
	if controlSet["returns"] && stackSet["unchanged"] && stackDelta != 0 {
		issues = append(issues, Issue{Severity: "error", Code: "returning-stack-leak", File: path, Line: fn.Line, Message: fmt.Sprintf("returning function leaves symbolic stack delta %d", stackDelta)})
	}
	if maxEntryStackPopDelta > 0 && !stackSet["switches"] && !stackSet["synthetic"] && !requireSet["stack.entry_pop.unchecked"] {
		issues = append(issues, Issue{Severity: "error", Code: "entry-stack-pop", File: path, Line: fn.Line, Message: fmt.Sprintf("function pops %d bytes above its entry stack before proving ownership; use stack.entry_pop.unchecked only after ABI proof", maxEntryStackPopDelta)})
	}
	if controlSet["returns"] && stackSet["unchanged"] && writesSP {
		issues = append(issues, Issue{Severity: "error", Code: "stack-pointer-write-unchanged", File: path, Line: fn.Line, Message: "stack: unchanged function writes the stack pointer directly"})
	}
	if stackSet["synthetic"] && usesCall && !usesJmp {
		issues = append(issues, Issue{Severity: "error", Code: "guest-entry-call-mangles-stack", File: path, Line: fn.Line, Message: "synthetic stack handoff must tail jump instead of call"})
	}
	if controlSet["noreturn"] && usesRet {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-can-return", File: path, Line: fn.Line, Message: "noreturn function contains ret"})
	}
	if controlSet["noreturn"] && !terminalOpIs(fn.Instructions, "jmp", "trap") {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-missing-terminal", File: path, Line: fn.Line, Message: "noreturn function must end in jmp or trap"})
	}
	if controlSet["noreturn"] && terminalOpIs(fn.Instructions, "jmp") && !controlSet["tail_jumps"] {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-jump-without-tail-contract", File: path, Line: fn.Line, Message: "noreturn jmp requires tail_jumps control contract"})
	}
	if controlSet["returns"] && usesJmp && !controlSet["tail_jumps"] {
		issues = append(issues, Issue{Severity: "error", Code: "returning-unqualified-jump", File: path, Line: fn.Line, Message: "returning function contains jmp without tail_jumps control contract"})
	}
	if controlSet["returns"] && !usesRet {
		issues = append(issues, Issue{Severity: "error", Code: "returns-missing-ret", File: path, Line: fn.Line, Message: "returning function must contain ret"})
	}
	if controlSet["returns"] && usesRet && !terminalOpIs(fn.Instructions, "ret") {
		issues = append(issues, Issue{Severity: "error", Code: "return-not-terminal", File: path, Line: fn.Line, Message: "returning function must end with ret"})
	}
	if controlSet["returns"] && hasCallImmediatelyBeforeRet(fn.Instructions) && !requireSet["call.return_address_choreography.unchecked"] {
		issues = append(issues, Issue{Severity: "error", Code: "call-immediately-before-ret", File: path, Line: fn.Line, Message: "call followed immediately by ret requires explicit call.return_address_choreography.unchecked intent"})
	}
	for _, reg := range []string{"rbx", "rbp", "r12", "r13", "r14", "r15"} {
		if clobberSet[reg] && !preserveSet[reg] && !preserveSet["callee_saved"] {
			issues = append(issues, Issue{Severity: "error", Code: "callee-saved-not-preserved", File: path, Line: fn.Line, Message: fmt.Sprintf("callee-saved register %s is clobbered without preservation contract", reg)})
		}
		if controlSet["returns"] && clobberSet[reg] && (preserveSet[reg] || preserveSet["callee_saved"]) && !requireSet["callee_saved.preservation.unchecked"] && !calleeSavedPushPopProven(fn.Instructions, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "callee-saved-preservation-unproven", File: path, Line: fn.Line, Message: fmt.Sprintf("callee-saved register %s preservation must be proven by push/pop or require callee_saved.preservation.unchecked", reg)})
		}
	}
	_ = target
	return issues
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
			if previous, ok := seenExports[fn.Name]; ok && fn.Name != "" {
				report.Issues = append(report.Issues, Issue{Severity: "error", Code: "duplicate-export", File: module.Path, Line: fn.Line, Message: fmt.Sprintf("EASM export %s duplicates export from %s", fn.Name, previous)})
			} else {
				seenExports[fn.Name] = module.Path
			}
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

func terminalOpIs(instructions []Instruction, ops ...string) bool {
	if len(instructions) == 0 {
		return false
	}
	last := normalizeOp(instructions[len(instructions)-1].Op)
	for _, op := range ops {
		if last == op {
			return true
		}
	}
	return false
}

func hasCallImmediatelyBeforeRet(instructions []Instruction) bool {
	for i := 1; i < len(instructions); i++ {
		prev := normalizeOp(instructions[i-1].Op)
		cur := normalizeOp(instructions[i].Op)
		if (prev == "call" || prev == "callq") && cur == "ret" {
			return true
		}
	}
	return false
}

func hasReturnOutput(outputs []string) bool {
	for _, output := range outputs {
		if bindingName(output) == "ret" && registerAfterEquals(output) != "" {
			return true
		}
	}
	return false
}

func outputRegisterSet(outputs []string) map[string]bool {
	out := map[string]bool{}
	for _, output := range outputs {
		if reg := registerAfterEquals(output); reg != "" {
			out[canonicalX86GPR(reg)] = true
		}
	}
	return out
}

func verifyABI(path string, fn *Function) []Issue {
	switch strings.ToLower(strings.TrimSpace(fn.ABI)) {
	case "", "c", "sysv", "sysv_x86_64", "ps4_sysv":
		return nil
	default:
		return []Issue{{Severity: "error", Code: "unknown-abi", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown EASM ABI %s", fn.ABI)}}
	}
}

func verifySignatureTypes(path string, fn *Function) []Issue {
	var issues []Issue
	if !allowedSignatureType(fn.ReturnType) {
		issues = append(issues, Issue{Severity: "error", Code: "invalid-signature-type", File: path, Line: fn.Line, Message: fmt.Sprintf("unsupported EASM return type %s", fn.ReturnType)})
	}
	for _, param := range fn.Params {
		if !allowedSignatureType(param.Type) || param.Type == "void" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-signature-type", File: path, Line: fn.Line, Message: fmt.Sprintf("unsupported EASM parameter type %s", param.Type)})
		}
	}
	return issues
}

func allowedSignatureType(name string) bool {
	switch strings.TrimSpace(name) {
	case "void", "bool", "char", "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		return true
	default:
		return false
	}
}

func verifyContractTokens(path string, fn *Function) []Issue {
	var issues []Issue
	for _, token := range contractFields(fn.Stack) {
		if !allowedStackToken(token) {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-stack-contract", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown stack contract %s", token)})
		}
	}
	for _, token := range contractFields(fn.Control) {
		if !allowedControlToken(token) {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-control-contract", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown control contract %s", token)})
		}
	}
	return issues
}

func contractFields(values []string) []string {
	var out []string
	for _, value := range values {
		fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\t' || r == '\n' })
		for _, field := range fields {
			for _, part := range strings.Fields(strings.TrimSpace(field)) {
				out = append(out, strings.ToLower(part))
			}
		}
	}
	return out
}

func allowedStackToken(token string) bool {
	switch token {
	case "unchanged", "aligned", "16", "synthetic", "switches", "noreturn", "probed":
		return true
	default:
		return false
	}
}

func allowedControlToken(token string) bool {
	switch token {
	case "returns", "noreturn", "tail_jumps", "may_fault":
		return true
	default:
		return false
	}
}

func verifyBindings(path string, target string, fn *Function) []Issue {
	var issues []Issue
	paramNames := map[string]bool{}
	for _, param := range fn.Params {
		paramNames[param.Name] = true
	}
	seenInputs := map[string]bool{}
	for _, input := range fn.Inputs {
		name := bindingName(input)
		if name == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("invalid input binding %q", input)})
			continue
		}
		if !paramNames[name] {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("input binding %s does not name a parameter", name)})
		}
		if seenInputs[name] {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("duplicate input binding %s", name)})
		}
		seenInputs[name] = true
		if reg := registerAfterEquals(input); reg == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-register-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("input binding %s must use = <register>", name)})
		} else if !targetAllowsRegister(target, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("input binding %s uses register %s outside target %s", name, reg, defaultString(target, "any"))})
		}
	}
	for _, param := range fn.Params {
		if !seenInputs[param.Name] {
			issues = append(issues, Issue{Severity: "error", Code: "missing-input-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("parameter %s must be declared in inputs", param.Name)})
		}
	}
	seenOutputs := map[string]bool{}
	for _, output := range fn.Outputs {
		name := bindingName(output)
		if name == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-output-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("invalid output binding %q", output)})
			continue
		}
		if name != "ret" {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-output-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("EASM v1 only supports ret output binding, got %s", name)})
		}
		if seenOutputs[name] {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-output-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("duplicate output binding %s", name)})
		}
		seenOutputs[name] = true
		if reg := registerAfterEquals(output); reg == "" {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-register-binding", File: path, Line: fn.Line, Message: fmt.Sprintf("output binding %s must use = <register>", name)})
		} else {
			if !targetAllowsRegister(target, reg) {
				issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("output binding %s uses register %s outside target %s", name, reg, defaultString(target, "any"))})
			}
			if name == "ret" && !returnTypeAllowsRegister(fn.ReturnType, reg) {
				issues = append(issues, Issue{Severity: "error", Code: "return-register-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("return type %s cannot use return register %s", fn.ReturnType, reg)})
			}
		}
	}
	return issues
}

func verifyRegisterLists(path string, target string, fn *Function) []Issue {
	var issues []Issue
	for _, clobber := range contractFields(fn.Clobbers) {
		if clobber == "memory" || clobber == "cc" || clobber == "flags" {
			continue
		}
		reg := strings.TrimPrefix(clobber, "%")
		if !isRegisterName(reg) {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-clobber-register", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown clobber register %s", clobber)})
			continue
		}
		if !targetAllowsRegister(target, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("clobber register %s is outside target %s", reg, defaultString(target, "any"))})
		}
	}
	for _, preserve := range contractFields(fn.Preserves) {
		if preserve == "callee_saved" {
			continue
		}
		reg := strings.TrimPrefix(preserve, "%")
		if !isRegisterName(reg) {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-preserve-register", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown preserve register %s", preserve)})
			continue
		}
		if !targetAllowsRegister(target, reg) {
			issues = append(issues, Issue{Severity: "error", Code: "register-target-mismatch", File: path, Line: fn.Line, Message: fmt.Sprintf("preserve register %s is outside target %s", reg, defaultString(target, "any"))})
		}
	}
	return issues
}

func verifyDuplicateContractAtoms(path string, fn *Function) []Issue {
	var issues []Issue
	sections := []struct {
		name   string
		values []string
	}{
		{name: "clobbers", values: fn.Clobbers},
		{name: "preserves", values: fn.Preserves},
		{name: "stack", values: fn.Stack},
		{name: "control", values: fn.Control},
		{name: "requires", values: fn.Requires},
	}
	for _, section := range sections {
		seen := map[string]bool{}
		for _, atom := range contractFields(section.values) {
			if seen[atom] {
				issues = append(issues, Issue{Severity: "error", Code: "duplicate-contract-atom", File: path, Line: fn.Line, Message: fmt.Sprintf("%s repeats %s", section.name, atom)})
			}
			seen[atom] = true
		}
	}
	clobbers := setOf(fn.Clobbers)
	for _, preserve := range contractFields(fn.Preserves) {
		if preserve == "callee_saved" {
			continue
		}
		if clobbers[preserve] {
			continue
		}
		issues = append(issues, Issue{Severity: "error", Code: "preserve-without-clobber", File: path, Line: fn.Line, Message: fmt.Sprintf("preserves declares %s but clobbers does not", preserve)})
	}
	return issues
}

func bindingName(value string) string {
	pieces := strings.SplitN(value, "=", 2)
	fields := strings.Fields(strings.TrimSpace(pieces[0]))
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

func registerAfterEquals(value string) string {
	pieces := strings.SplitN(value, "=", 2)
	if len(pieces) != 2 {
		return ""
	}
	reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(pieces[1])), "%")
	if isRegisterName(reg) {
		return reg
	}
	return ""
}

func isRegisterName(value string) bool {
	reg := strings.ToLower(strings.TrimPrefix(value, "%"))
	if isXMMRegister(reg) || isAArch64SIMDRegister(reg) {
		return true
	}
	switch reg {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi", "rsp", "rbp", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15",
		"eax", "ebx", "ecx", "edx", "esi", "edi", "esp", "ebp",
		"al", "ah", "ax", "bl", "bh", "bx", "cl", "ch", "cx", "dl", "dh", "dx",
		"x0", "x1", "x2", "x3", "x4", "x5", "x6", "x7", "x8", "x9", "x10", "x11", "x12", "x13", "x14", "x15", "x16", "x17", "x18", "x19", "x20", "x21", "x22", "x23", "x24", "x25", "x26", "x27", "x28", "x29", "x30",
		"w0", "w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8", "w9", "w10", "w11", "w12", "w13", "w14", "w15", "w16", "w17", "w18", "w19", "w20", "w21", "w22", "w23", "w24", "w25", "w26", "w27", "w28", "w29", "w30",
		"sp":
		return true
	default:
		return false
	}
}

func targetAllowsRegister(target string, reg string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	if target == "" || target == "any" {
		return true
	}
	switch {
	case strings.Contains(target, "x86_64") || strings.Contains(target, "amd64"):
		return isX86Register(reg)
	case strings.Contains(target, "aarch64") || strings.Contains(target, "arm64"):
		return isAArch64Register(reg)
	default:
		return true
	}
}

func returnTypeAllowsRegister(returnType string, reg string) bool {
	returnType = strings.TrimSpace(returnType)
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	switch returnType {
	case "f32", "f64":
		return isXMMRegister(reg) || isAArch64SIMDRegister(reg)
	case "void":
		return false
	default:
		return isIntegerReturnRegister(reg)
	}
}

func isIntegerReturnRegister(reg string) bool {
	switch reg {
	case "rax", "eax", "ax", "al", "x0", "w0":
		return true
	default:
		return false
	}
}

func isX86Register(reg string) bool {
	if isXMMRegister(reg) {
		return true
	}
	return isX86GPR(reg)
}

func isX86GPR(reg string) bool {
	reg = canonicalX86GPR(reg)
	switch reg {
	case "rax", "rbx", "rcx", "rdx", "rsi", "rdi", "rsp", "rbp", "r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15":
		return true
	default:
		return false
	}
}

func isAArch64Register(reg string) bool {
	if reg == "sp" {
		return true
	}
	if isAArch64SIMDRegister(reg) {
		return true
	}
	if len(reg) < 2 {
		return false
	}
	prefix := reg[0]
	if prefix != 'x' && prefix != 'w' {
		return false
	}
	n, err := strconv.Atoi(reg[1:])
	return err == nil && n >= 0 && n <= 30
}

func isXMMRegister(reg string) bool {
	if !strings.HasPrefix(reg, "xmm") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(reg, "xmm"))
	return err == nil && n >= 0 && n <= 31
}

func isAArch64SIMDRegister(reg string) bool {
	if len(reg) < 2 {
		return false
	}
	prefix := reg[0]
	if prefix != 's' && prefix != 'd' && prefix != 'v' && prefix != 'q' {
		return false
	}
	n, err := strconv.Atoi(reg[1:])
	return err == nil && n >= 0 && n <= 31
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

func usesStackRegister(text string) bool {
	for _, operand := range splitInstructionOperands(text) {
		for _, token := range registerTokens(operand) {
			switch token {
			case "rsp", "esp", "sp":
				return true
			}
		}
	}
	return false
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
		return []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11", "cc"}
	default:
		return nil
	}
}

func callerSavedGPRs() []string {
	return []string{"rax", "rcx", "rdx", "rsi", "rdi", "r8", "r9", "r10", "r11"}
}

func instructionReadsRegisterBeforeWriting(text string, reg string) bool {
	reg = canonicalX86GPR(reg)
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	readOperands := parts
	if len(parts) >= 2 {
		dst := strings.TrimSpace(parts[len(parts)-1])
		dstReg := canonicalX86GPR(strings.TrimPrefix(strings.ToLower(dst), "%"))
		if dstReg == reg && instructionOverwritesDestination(text) {
			readOperands = parts[:len(parts)-1]
		}
	}
	for _, operand := range readOperands {
		for _, token := range registerTokens(operand) {
			if canonicalX86GPR(token) == reg {
				return true
			}
		}
	}
	return false
}

func instructionOverwritesDestination(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	switch normalizeOp(fields[0]) {
	case "mov", "movq", "movl", "movw", "movb", "movsx", "movsxd", "movzx", "lea", "pop", "popq":
		return true
	case "xor", "xorq":
		parts := splitInstructionOperands(text)
		return len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	default:
		return false
	}
}

func writtenRegister(text string) string {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return ""
	}
	fields := strings.Fields(text)
	op := ""
	if len(fields) > 0 {
		op = normalizeOp(fields[0])
	}
	if strings.HasPrefix(op, "push") || op == "call" || op == "callq" || op == "jmp" || op == "ret" {
		return ""
	}
	if op == "cmp" || op == "cmpq" || op == "test" || op == "testq" || isConditionalJump(op) {
		return ""
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	dst = strings.TrimPrefix(strings.ToLower(dst), "%")
	if isRegisterName(dst) {
		return dst
	}
	return ""
}

func calleeSavedPushPopProven(instructions []Instruction, reg string) bool {
	reg = canonicalX86GPR(reg)
	firstWrite := -1
	lastWrite := -1
	firstPush := -1
	lastPop := -1
	for i, inst := range instructions {
		op := normalizeOp(inst.Op)
		parts := splitInstructionOperands(inst.Text)
		if len(parts) == 1 {
			operand := canonicalX86GPR(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[0])), "%"))
			if (op == "push" || op == "pushq") && operand == reg && firstPush < 0 {
				firstPush = i
			}
			if (op == "pop" || op == "popq") && operand == reg {
				lastPop = i
			}
		}
		if op != "pop" && op != "popq" && canonicalX86GPR(writtenRegister(inst.Text)) == reg {
			if firstWrite < 0 {
				firstWrite = i
			}
			lastWrite = i
		}
	}
	if firstWrite < 0 {
		return true
	}
	return firstPush >= 0 && firstPush < firstWrite && lastPop > lastWrite
}

func canonicalX86GPR(reg string) string {
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	switch reg {
	case "eax", "ax", "al", "ah":
		return "rax"
	case "ebx", "bx", "bl", "bh":
		return "rbx"
	case "ecx", "cx", "cl", "ch":
		return "rcx"
	case "edx", "dx", "dl", "dh":
		return "rdx"
	case "esi":
		return "rsi"
	case "edi":
		return "rdi"
	case "esp":
		return "rsp"
	case "ebp":
		return "rbp"
	default:
		return reg
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

func hasAmbiguousOperandSize(op string, text string) bool {
	if strings.HasSuffix(op, "b") || strings.HasSuffix(op, "w") || strings.HasSuffix(op, "l") || strings.HasSuffix(op, "q") {
		return false
	}
	switch op {
	case "mov", "add", "sub", "and", "xor", "cmp", "test", "inc", "dec":
		return strings.Contains(text, "(") || strings.Contains(text, "$")
	default:
		return false
	}
}

func immediateOverflowsInstruction(op string, text string) bool {
	width := instructionWidthBits(op)
	if width == 0 {
		return false
	}
	for _, operand := range splitInstructionOperands(text) {
		value, ok := parseImmediate(operand)
		if !ok {
			continue
		}
		min := -(int64(1) << (width - 1))
		maxUnsigned := uint64(1)<<width - 1
		if value < 0 {
			if value < min {
				return true
			}
			continue
		}
		if uint64(value) > maxUnsigned {
			return true
		}
	}
	return false
}

func instructionWidthBits(op string) uint {
	switch {
	case strings.HasSuffix(op, "b"):
		return 8
	case strings.HasSuffix(op, "w"):
		return 16
	case strings.HasSuffix(op, "l"):
		return 32
	default:
		return 0
	}
}

func parseImmediate(operand string) (int64, bool) {
	operand = strings.TrimSpace(strings.TrimPrefix(operand, "$"))
	if operand == "" {
		return 0, false
	}
	if strings.HasPrefix(operand, "-") {
		value, err := strconv.ParseInt(operand, 0, 64)
		return value, err == nil
	}
	value, err := strconv.ParseUint(operand, 0, 64)
	if err != nil {
		return 0, false
	}
	if value > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), true
	}
	return int64(value), true
}

func usesSymbolAddress(text string) bool {
	fields := strings.Fields(text)
	if len(fields) > 0 && (isConditionalJump(normalizeOp(fields[0])) || normalizeOp(fields[0]) == "jmp" || strings.HasPrefix(normalizeOp(fields[0]), "call")) {
		return false
	}
	if len(fields) > 0 && normalizeOp(fields[0]) == "mrs" {
		return false
	}
	for _, operand := range splitInstructionOperands(text) {
		operand = strings.TrimSpace(strings.TrimPrefix(operand, "$"))
		operand = strings.TrimPrefix(operand, "*")
		if operand == "" || strings.HasPrefix(operand, "%") || strings.HasPrefix(operand, "0x") || strings.HasPrefix(operand, "-") {
			continue
		}
		if strings.Contains(operand, "(") {
			base := strings.TrimSpace(operand[:strings.Index(operand, "(")])
			return base != "" && !isNumericLiteral(base)
		}
		if isIdentifierLike(operand) {
			return true
		}
	}
	return false
}

func usesSegmentOverride(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "%fs:") || strings.Contains(lower, "%gs:") || strings.Contains(lower, "fs:") || strings.Contains(lower, "gs:")
}

func segmentOverridesUsed(text string) map[string]bool {
	lower := strings.ToLower(text)
	used := map[string]bool{}
	if strings.Contains(lower, "%fs:") || strings.Contains(lower, "fs:") {
		used["fs"] = true
	}
	if strings.Contains(lower, "%gs:") || strings.Contains(lower, "gs:") {
		used["gs"] = true
	}
	return used
}

func hasSegmentCapability(requireSet map[string]bool, seg string) bool {
	return requireSet["x86_64.segment"] || requireSet["x86_64.segment."+seg]
}

func usesSegmentRegister(text string) bool {
	return len(segmentRegistersUsed(text)) != 0
}

func segmentRegistersUsed(text string) map[string]bool {
	used := map[string]bool{}
	for _, operand := range splitInstructionOperands(text) {
		reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(operand)), "%")
		if reg == "fs" {
			used["fs"] = true
		}
		if reg == "gs" {
			used["gs"] = true
		}
	}
	return used
}

func writesSegmentRegister(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[len(parts)-1])), "%")
	return dst == "fs" || dst == "gs"
}

func isIndirectControlTransfer(op string, text string) bool {
	if op != "jmp" && op != "call" && op != "callq" {
		return false
	}
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(parts[0]), "*")
}

func isDirectSymbolControlTransfer(op string, text string) bool {
	if op != "jmp" && op != "call" && op != "callq" {
		return false
	}
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	target := strings.TrimSpace(parts[0])
	if target == "" || strings.HasPrefix(target, "*") || strings.HasPrefix(target, "%") || strings.HasPrefix(target, "$") || isNumericLiteral(target) {
		return false
	}
	return isIdentifierLike(target)
}

func stackAndAlignsTo16(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[len(parts)-1])), "%")
	if dst != "rsp" && dst != "esp" {
		return false
	}
	value, ok := parseImmediate(parts[0])
	return ok && value == -16
}

func mod16(value int) int {
	value %= 16
	if value < 0 {
		value += 16
	}
	return value
}

func usesReservedRegister(target string, text string, requireSet map[string]bool) bool {
	target = strings.ToLower(target)
	switch {
	case strings.Contains(target, "aarch64") || strings.Contains(target, "arm64"):
		return instructionUsesRegister(text, "x18") && !requireSet["aarch64.platform_register.x18"]
	case strings.Contains(target, "riscv"):
		return (instructionUsesRegister(text, "gp") || instructionUsesRegister(text, "tp")) && !requireSet["riscv.reserved_registers"]
	default:
		return false
	}
}

func instructionUsesRegister(text string, reg string) bool {
	reg = strings.ToLower(strings.TrimPrefix(reg, "%"))
	for _, operand := range splitInstructionOperands(text) {
		for _, token := range registerTokens(operand) {
			if token == reg {
				return true
			}
		}
	}
	return false
}

func registerTokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r == '%' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	var out []string
	for _, field := range fields {
		field = strings.TrimPrefix(field, "%")
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func writesMemory(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	op := normalizeOp(strings.Fields(strings.TrimSpace(text))[0])
	if op == "xchg" || op == "xchgl" || op == "xchgq" {
		return hasMemoryOperand(text)
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	return strings.Contains(dst, "(") && strings.Contains(dst, ")")
}

func hasMemoryOperand(text string) bool {
	for _, operand := range splitInstructionOperands(text) {
		if strings.Contains(operand, "(") && strings.Contains(operand, ")") {
			return true
		}
	}
	return usesSegmentOverride(text)
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

func isIdentifierLike(value string) bool {
	for i, r := range value {
		if i == 0 {
			if !(r == '_' || r == '.' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
				return false
			}
			continue
		}
		if !(r == '_' || r == '.' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return value != ""
}

func isNumericLiteral(value string) bool {
	_, err := strconv.ParseInt(value, 0, 64)
	if err == nil {
		return true
	}
	_, err = strconv.ParseUint(value, 0, 64)
	return err == nil
}

func writesPartialReturnRegister(text string) bool {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return false
	}
	dst := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[len(parts)-1])), "%")
	switch dst {
	case "al", "ah", "ax":
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
	var out []string
	var current strings.Builder
	depth := 0
	for _, r := range operandText {
		switch r {
		case '(':
			depth++
			current.WriteRune(r)
		case ')':
			if depth > 0 {
				depth--
			}
			current.WriteRune(r)
		case ',':
			if depth == 0 {
				part := strings.TrimSpace(current.String())
				if part != "" {
					out = append(out, part)
				}
				current.Reset()
				continue
			}
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	part := strings.TrimSpace(current.String())
	if part != "" {
		out = append(out, part)
	}
	return out
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
