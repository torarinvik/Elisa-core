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
	Fragments []Function
	Protocols []Protocol
	Templates []Template
}

// Template is a routine assembled at build time into bytes with typed runtime-filled holes
// (docs/101 §3). A hole is just a parameter filled at instantiate time rather than call time;
// the body references it by bare name, so a template reads like an ordinary Elisa function.
type Template struct {
	Name         string
	Holes        []Param
	Instructions []Instruction
	PatchPoints  []PatchPoint
	Line         int
}

// PatchPoint records where a typed hole is consumed in the assembled body.
type PatchPoint struct {
	Hole       string
	Type       string
	InstrIndex int
	Class      string // "sel16" | "wide64" | ""
}

// Protocol declares a machine operation by signature; each platform-tagged function whose
// name matches a method is an impl, and selectPlatformImpls keeps the one matching the build
// target. This is the (arch × os × role) dispatch layer of docs/101 §2.
type Protocol struct {
	Name    string
	Methods []protocolMethod
	Line    int
}

type protocolMethod struct {
	Name   string
	Arity  int
	Return string
}

type Function struct {
	Name         string
	Params       []Param
	ReturnType   string
	ABI          string
	Effects      []string
	Facts        []string
	Inputs       []string
	Outputs      []string
	Labels       []LabelContract
	Clobbers     []string
	Preserves    []string
	Stack        []string
	Control      []string
	Requires     []string
	Changes      []string
	Reads        []string
	Instructions []Instruction
	Line         int
	IsFragment   bool
	Platform     string
}

type Param struct {
	Name string
	Type string
}

type Instruction struct {
	Op     string
	Text   string
	Line   int
	Label  string
	Pseudo bool
}

type LabelContract struct {
	Name          string
	Preconditions []string
	Line          int
}

type machineFactState struct {
	LiveRegs        map[string]bool
	KnownUInt       map[string]uint64
	FS              string
	StackMod16      int
	StackMod16Known bool
	StackTopValue   uint64
	StackTopKnown   bool
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
	Facts      []string `json:"facts,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Control    []string `json:"control,omitempty"`
	Stack      []string `json:"stack,omitempty"`
	// DerivedEffects is the caller-facing Unsafe.*/Segment.* set projected from the verified
	// contract (docs/101 §4) — surfaced for honesty and migration planning.
	DerivedEffects []string `json:"derivedEffects,omitempty"`
}

var (
	exportHeaderRE   = regexp.MustCompile(`^export\s+def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*->\s*([A-Za-z_][A-Za-z0-9_\[\]&?]*)\s*(.*):\s*$`)
	fragmentHeaderRE = regexp.MustCompile(`^fragment\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*:\s*$`)
	protocolHeaderRE = regexp.MustCompile(`^protocol\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*$`)
	templateHeaderRE = regexp.MustCompile(`^template\s+def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*:\s*$`)
	identTokenRE     = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	protocolMethodRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*->\s*([A-Za-z_][A-Za-z0-9_\[\]&?]*)\s*$`)
	platformForRE    = regexp.MustCompile(`\bfor\b\s+(\([^)]*\)|[^\s]+)`)
	nativeCanRE      = regexp.MustCompile(`\bcan\b\s+([^\[].*?)(?:\s+abi\b|\s+for\b|$)`)
	sectionRE        = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*:\s*(.*)$`)
	allowedOps     = map[string]bool{
		"mov": true, "movq": true, "lea": true, "push": true, "pushq": true, "pop": true, "popq": true,
		"movb": true, "movw": true, "movl": true,
		"movsx": true, "movsxd": true, "movsbw": true, "movsbl": true, "movsbq": true, "movswl": true, "movswq": true, "movslq": true,
		"movzx": true, "movzbw": true, "movzbl": true, "movzbq": true, "movzwl": true, "movzwq": true,
		"xchg": true, "xchgl": true, "xchgq": true,
		"add": true, "addq": true, "sub": true, "subq": true, "and": true, "andq": true,
		"cmp": true, "cmpq": true, "test": true, "testq": true, "inc": true, "incq": true, "dec": true, "decq": true,
		"xor": true, "xorq": true, "call": true, "callq": true, "jmp": true, "jmpq": true, "ret": true, "retq": true,
		"cpuid": true, "cld": true, "std": true,
		"lfence": true, "rdtsc": true, "pause": true, "yield": true, "mrs": true, "isb": true,
		"fldcw": true, "fnstcw": true, "stmxcsr": true, "ldmxcsr": true, "emms": true,
		"vzeroall": true, "trap": true,
	}
	capabilityByOp = map[string]string{
		"rdtsc": "x86_64.rdtsc", "lfence": "x86_64.sse.lfence", "pause": "x86_64.sse.pause", "yield": "aarch64.yield",
		"cpuid": "x86_64.cpuid",
		"xchg":  "x86_64.atomic.rmw", "xchgl": "x86_64.atomic.rmw", "xchgq": "x86_64.atomic.rmw",
		"mrs": "aarch64.cntvct", "isb": "aarch64.cntvct", "fldcw": "x86_64.fpu_control",
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
	var currentProto *Protocol
	var currentTemplate *Template
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
		if currentProto != nil {
			if m := protocolMethodRE.FindStringSubmatch(line); m != nil {
				currentProto.Methods = append(currentProto.Methods, protocolMethod{Name: m[1], Arity: len(splitCSV(m[2])), Return: m[3]})
				continue
			}
			currentProto = nil
		}
		if currentTemplate != nil {
			if !isHeaderLine(line) {
				if inst, ok := parseBodyInstruction(line, lineNo); ok {
					currentTemplate.Instructions = append(currentTemplate.Instructions, inst)
				}
				continue
			}
			currentTemplate = nil
		}
		if current == nil {
			switch {
			case strings.HasPrefix(line, "module "):
				module.Name = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			case strings.HasPrefix(line, "target "):
				module.Target = strings.TrimSpace(strings.TrimPrefix(line, "target "))
			case protocolHeaderRE.MatchString(line):
				m := protocolHeaderRE.FindStringSubmatch(line)
				module.Protocols = append(module.Protocols, Protocol{Name: m[1], Line: lineNo})
				currentProto = &module.Protocols[len(module.Protocols)-1]
				section = ""
			case exportHeaderRE.MatchString(line):
				fn, issue := parseFunctionHeader(path, lineNo, line)
				if issue != nil {
					issues = append(issues, *issue)
					continue
				}
				module.Functions = append(module.Functions, fn)
				current = &module.Functions[len(module.Functions)-1]
				section = ""
			case fragmentHeaderRE.MatchString(line):
				fr, issue := parseFragmentHeader(path, lineNo, line)
				if issue != nil {
					issues = append(issues, *issue)
					continue
				}
				module.Fragments = append(module.Fragments, fr)
				current = &module.Fragments[len(module.Fragments)-1]
				section = ""
			case templateHeaderRE.MatchString(line):
				tpl, issue := parseTemplateHeader(path, lineNo, line)
				if issue != nil {
					issues = append(issues, *issue)
					continue
				}
				module.Templates = append(module.Templates, tpl)
				currentTemplate = &module.Templates[len(module.Templates)-1]
				section = ""
			default:
				issues = append(issues, Issue{Severity: "error", Code: "unexpected-top-level", File: path, Line: lineNo, Message: "expected module, target, export def, fragment, protocol, or template def"})
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
		if fragmentHeaderRE.MatchString(line) {
			fr, issue := parseFragmentHeader(path, lineNo, line)
			if issue != nil {
				issues = append(issues, *issue)
				continue
			}
			module.Fragments = append(module.Fragments, fr)
			current = &module.Fragments[len(module.Fragments)-1]
			section = ""
			continue
		}
		if protocolHeaderRE.MatchString(line) {
			m := protocolHeaderRE.FindStringSubmatch(line)
			module.Protocols = append(module.Protocols, Protocol{Name: m[1], Line: lineNo})
			currentProto = &module.Protocols[len(module.Protocols)-1]
			current = nil
			section = ""
			continue
		}
		if templateHeaderRE.MatchString(line) {
			tpl, issue := parseTemplateHeader(path, lineNo, line)
			if issue != nil {
				issues = append(issues, *issue)
				continue
			}
			module.Templates = append(module.Templates, tpl)
			currentTemplate = &module.Templates[len(module.Templates)-1]
			current = nil
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
		// Native inline contract — `requires x86_64.segment.gs`, `clobbers rax, cc`, `can Unsafe.X`
		// — a contract keyword with values on one line, no colon and no `body:` ceremony.
		if kw, rest, ok := inlineContract(line); ok {
			if kw == "can" {
				current.Effects = append(current.Effects, splitCSV(rest)...)
			} else {
				addSectionValue(current, kw, rest, lineNo)
			}
			section = "body"
			continue
		}
		// A bare line inside an open colon-section is a continuation value (legacy multi-line form).
		if section != "" && section != "body" {
			addSectionValue(current, section, lineWithIndent(raw), lineNo)
			continue
		}
		// Otherwise it is a body instruction. In the native form, instructions just begin — no
		// `body:` marker required.
		section = "body"
		addSectionValue(current, "body", lineWithIndent(raw), lineNo)
	}
	if err := scanner.Err(); err != nil {
		issues = append(issues, Issue{Severity: "error", Code: "scan-failed", File: path, Message: err.Error()})
	}
	if strings.TrimSpace(module.Name) == "" {
		module.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	issues = append(issues, expandCompositions(module)...)
	issues = append(issues, selectPlatformImpls(module)...)
	issues = append(issues, analyzeTemplates(module)...)
	return module, append(issues, VerifyModule(module)...)
}

func isHeaderLine(line string) bool {
	return strings.HasPrefix(line, "module ") || strings.HasPrefix(line, "target ") ||
		exportHeaderRE.MatchString(line) || fragmentHeaderRE.MatchString(line) ||
		protocolHeaderRE.MatchString(line) || templateHeaderRE.MatchString(line)
}

func parseTemplateHeader(path string, line int, text string) (Template, *Issue) {
	m := templateHeaderRE.FindStringSubmatch(text)
	if m == nil {
		return Template{}, &Issue{Severity: "error", Code: "invalid-template", File: path, Line: line, Message: "invalid template header"}
	}
	tpl := Template{Name: m[1], Line: line}
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
			return tpl, &Issue{Severity: "error", Code: "duplicate-hole", File: path, Line: line, Message: fmt.Sprintf("duplicate template hole %s", name)}
		}
		seen[name] = true
		tpl.Holes = append(tpl.Holes, Param{Name: name, Type: typ})
	}
	return tpl, nil
}

// analyzeTemplates records, for each template, where its typed holes are consumed, and checks
// that (1) every declared hole is referenced and (2) a hole's type fits the operand slot it
// lands in — a 16-bit selector cannot be baked where a 64-bit address/target is expected, and
// vice versa. This is the type-safe half of runtime code-gen; the byte assembly is Stage 4b.
func analyzeTemplates(module *Module) []Issue {
	if module == nil {
		return nil
	}
	var issues []Issue
	for ti := range module.Templates {
		tpl := &module.Templates[ti]
		holeType := map[string]string{}
		for _, h := range tpl.Holes {
			holeType[h.Name] = h.Type
		}
		referenced := map[string]bool{}
		var pps []PatchPoint
		for idx, inst := range tpl.Instructions {
			if inst.Op == "compose" {
				issues = append(issues, Issue{Severity: "error", Code: "template-compose", File: module.Path, Line: inst.Line, Message: "templates may not compose fragments"})
				continue
			}
			class := operandClassForMnemonic(strings.ToLower(strings.TrimSpace(inst.Op)))
			for _, tok := range identTokenRE.FindAllString(inst.Text, -1) {
				typ, ok := holeType[tok]
				if !ok {
					continue
				}
				referenced[tok] = true
				if hc := holeClass(typ); class != "" && hc != "" && class != hc {
					issues = append(issues, Issue{Severity: "error", Code: "template-hole-type-mismatch", File: module.Path, Line: inst.Line, Message: fmt.Sprintf("hole %s (%s) is %s but %s expects a %s operand", tok, typ, hc, inst.Op, class)})
				}
				pps = append(pps, PatchPoint{Hole: tok, Type: typ, InstrIndex: idx, Class: class})
			}
		}
		for _, h := range tpl.Holes {
			if !referenced[h.Name] {
				issues = append(issues, Issue{Severity: "error", Code: "unused-template-hole", File: module.Path, Line: tpl.Line, Message: fmt.Sprintf("template hole %s is declared but never referenced", h.Name)})
			}
		}
		tpl.PatchPoints = pps
	}
	return issues
}

func operandClassForMnemonic(mnemonic string) string {
	switch mnemonic {
	case "movw", "fldcw", "fnstcw":
		return "sel16"
	case "movabs", "movq", "lea", "call", "callq", "jmp", "jmpq", "push", "pushq":
		return "wide64"
	default:
		return ""
	}
}

func holeClass(typ string) string {
	switch strings.TrimSpace(typ) {
	case "u16", "i16", "GuestFsSelector", "HostFsSelector", "GuestGsSelector", "HostGsSelector":
		return "sel16"
	case "":
		return ""
	default:
		return "wide64"
	}
}

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
	for name, arg := range subst {
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
	returnReg := returnOutputRegister(fn.Outputs)
	inputRegs := inputRegisterSet(fn.Inputs)
	inputRegNames := inputRegisterNameSet(fn.Inputs)
	labelContracts := labelContractMap(fn.Labels)
	definedLabels := bodyLabelDefinitions(fn.Instructions)
	seenLabelDefinitions := map[string]int{}
	state := machineFactState{LiveRegs: map[string]bool{}, KnownUInt: map[string]uint64{}}
	for reg := range inputRegs {
		state.LiveRegs[reg] = true
	}
	state = applyMachineFactPreconditions(state, fn.Facts)
	returnsVoid := strings.TrimSpace(fn.ReturnType) == "void"
	stackDelta := 0
	maxEntryStackPopDelta := 0
	stackMod16 := 8
	stackMod16Known := strings.Contains(strings.ToLower(target), "x86_64") || strings.Contains(strings.ToLower(target), "amd64")
	state.StackMod16 = stackMod16
	state.StackMod16Known = stackMod16Known
	maxStackAllocation := 0
	touchesStack := false
	mutatesStack := false
	usesCall := false
	usesJmp := false
	usesRet := false
	writesSP := false
	writesSPFromOwnedInput := false
	writesSegment := false
	mayFault := false
	flagsLive := false
	wrotePartialReturn := false
	directionFlagSet := false
	rdtscSeen := false
	lfenceBeforeRDTSC := false
	lfenceAfterRDTSC := false
	lfenceSeen := false
	clobberedByCall := map[string]int{}
	returnRegWritten := false
	inputRegRead := map[string]bool{}
	inputRegOverwritten := map[string]bool{}
	for _, inst := range fn.Instructions {
		op := normalizeOp(inst.Op)
		if inst.Label != "" {
			if firstLine, exists := seenLabelDefinitions[inst.Label]; exists {
				issues = append(issues, Issue{Severity: "error", Code: "duplicate-label", File: path, Line: inst.Line, Message: fmt.Sprintf("label %s duplicates earlier label on line %d", inst.Label, firstLine)})
			} else {
				seenLabelDefinitions[inst.Label] = inst.Line
			}
			if contract, ok := labelContracts[inst.Label]; ok {
				state = withStackMod16(state, stackMod16, stackMod16Known)
				issues = append(issues, checkLabelPreconditions(path, inst.Line, "fallthrough", inst.Label, contract.Preconditions, state)...)
				state = applyMachineFactPreconditions(machineFactState{}, contract.Preconditions)
				stackMod16 = state.StackMod16
				stackMod16Known = state.StackMod16Known
			}
			continue
		}
		if inst.Pseudo && normalizeOp(inst.Op) == "state" {
			state = applyMachineStateAssertion(state, inst.Text)
			if stack, ok := stackMod16FactPrecondition(inst.Text); ok {
				stackMod16 = stack
				stackMod16Known = true
			}
			continue
		}
		if inst.Pseudo {
			continue
		}
		if !allowedOps[op] && !isConditionalJump(op) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-instruction", File: path, Line: inst.Line, Message: fmt.Sprintf("unsupported EASM instruction %q", inst.Op)})
		}
		if cap := capabilityByOp[op]; cap != "" && !requireSet[cap] {
			issues = append(issues, Issue{Severity: "error", Code: "missing-capability", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q requires capability %s", inst.Op, cap)})
		}
		if usesStackRegister(inst.Text) || strings.HasPrefix(op, "push") || strings.HasPrefix(op, "pop") {
			touchesStack = true
		}
		if writesMemory(inst.Text) && !clobberSet["memory"] && !clobberSet["memory.write"] {
			issues = append(issues, Issue{Severity: "error", Code: "memory-write-without-clobber", File: path, Line: inst.Line, Message: "memory write requires a memory or memory.write clobber"})
		}
		if readsMemory(inst.Text) && !clobberSet["memory"] && !clobberSet["memory.read"] {
			issues = append(issues, Issue{Severity: "error", Code: "memory-read-without-clobber", File: path, Line: inst.Line, Message: "memory load requires a memory or memory.read clobber"})
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
		if writesSegmentRegister(inst.Text) {
			writesSegment = true
			state.FS = ""
		}
		// Every register an instruction reads must be established before this point -- a
		// declared input, written earlier in the body, a preserved callee-saved value, or a
		// structural machine register (rsp/rip). Reading anything else consumes whatever
		// indeterminate value the caller happened to leave. Registers a prior call trashed
		// are owned by the more specific caller-saved-use-after-call check below.
		for _, reg := range registersReadBy(inst.Text) {
			if reg == "rsp" || reg == "rip" {
				continue
			}
			if _, trashedByCall := clobberedByCall[reg]; trashedByCall {
				continue
			}
			if state.LiveRegs[reg] || preserveSet[reg] || preserveSet["callee_saved"] {
				continue
			}
			issues = append(issues, Issue{Severity: "error", Code: "register-read-uninitialized", File: path, Line: inst.Line, Message: fmt.Sprintf("register %s is read but not established (declare it as an input, write it earlier, or preserve it)", reg)})
		}
		for _, written := range writtenRegisters(inst.Text) {
			canonical := canonicalX86GPR(written)
			if isX86GPR(canonical) {
				state.LiveRegs[canonical] = true
				updateKnownRegisterValue(state, inst.Text, canonical)
			}
			if returnReg != "" && canonical == returnReg {
				returnRegWritten = true
			}
			if inputRegs[canonical] && !inputRegRead[canonical] {
				inputRegOverwritten[canonical] = true
			}
			if isX86GPR(canonical) && !clobberSet[canonical] && !outputSet[canonical] {
				issues = append(issues, Issue{Severity: "error", Code: "register-write-without-clobber", File: path, Line: inst.Line, Message: fmt.Sprintf("register %s is written but not declared as a clobber or output", canonical)})
			}
		}
		if isIndirectControlTransfer(op, inst.Text) && !requireSet["control.indirect"] {
			issues = append(issues, Issue{Severity: "error", Code: "indirect-control-intent-missing", File: path, Line: inst.Line, Message: "indirect call/jmp requires control.indirect intent"})
		}
		if isIndirectControlTransfer(op, inst.Text) && !requireSet["control.tiny_target.unchecked"] {
			if reg := indirectControlTargetRegister(inst.Text); reg != "" {
				if value, ok := state.KnownUInt[reg]; ok && value > 0 && value < 0x10000 {
					issues = append(issues, Issue{Severity: "error", Code: "tiny-indirect-control-target", File: path, Line: inst.Line, Message: fmt.Sprintf("indirect %s target %s is known tiny value 0x%x; require control.tiny_target.unchecked only for intentional sentinels", op, reg, value)})
				}
			}
		}
		if isIndirectControlTransfer(op, inst.Text) && !requireSet["control.poison_target.unchecked"] {
			if reg := indirectControlTargetRegister(inst.Text); reg != "" {
				if value, ok := state.KnownUInt[reg]; ok && isNonCanonicalX86Address(value) {
					issues = append(issues, Issue{Severity: "error", Code: "poison-indirect-control-target", File: path, Line: inst.Line, Message: fmt.Sprintf("indirect %s target %s is known non-canonical poison-like value 0x%x; require control.poison_target.unchecked only for intentional tests", op, reg, value)})
				}
			}
		}
		if isDirectSymbolControlTransfer(op, inst.Text) && !definedLabels[directControlTarget(op, inst.Text)] && !requireSet["control.direct"] && !requireSet["relocation.symbol"] {
			issues = append(issues, Issue{Severity: "error", Code: "direct-control-intent-missing", File: path, Line: inst.Line, Message: "direct symbolic call/jmp requires control.direct or relocation.symbol intent"})
		}
		if writesPartialReturnRegister(inst.Text) {
			wrotePartialReturn = true
		}
		if usesReservedRegister(target, inst.Text, requireSet) {
			issues = append(issues, Issue{Severity: "error", Code: "reserved-register-use", File: path, Line: inst.Line, Message: "target-reserved register requires an explicit platform capability"})
		}
		// Implicit reads (e.g. cpuid's leaf in eax / subleaf in ecx) are invisible in the
		// operand text. Require each to be a declared input or written earlier so a function
		// cannot silently consume an indeterminate caller-left value. Checked before the
		// implicit-clobber pass below, which then marks the same registers dead for
		// subsequent instructions.
		for _, reg := range implicitUses(op) {
			canonical := canonicalX86GPR(reg)
			if inputRegs[canonical] {
				inputRegRead[canonical] = true
				continue
			}
			if state.LiveRegs[canonical] {
				continue
			}
			issues = append(issues, Issue{Severity: "error", Code: "implicit-read-uninitialized", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q implicitly reads %s, which is not a declared input or written earlier in the body", inst.Op, canonical)})
		}
		for _, reg := range implicitClobbers(op) {
			canonical := canonicalX86GPR(reg)
			if implicitResultDefines(op) && isX86GPR(canonical) {
				state.LiveRegs[canonical] = true // instruction writes a defined result here
			} else {
				delete(state.LiveRegs, canonical)
			}
			delete(state.KnownUInt, canonical)
			if returnReg != "" && canonicalX86GPR(reg) == returnReg {
				returnRegWritten = true
			}
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
		for reg := range inputRegs {
			if !inputRegRead[reg] && !inputRegOverwritten[reg] && instructionReadsRegisterBeforeWriting(inst.Text, reg) {
				inputRegRead[reg] = true
			}
		}
		if instructionClobbersFlags(op) && !clobberSet["cc"] && !clobberSet["flags"] {
			issues = append(issues, Issue{Severity: "error", Code: "cc-clobber-missing", File: path, Line: inst.Line, Message: fmt.Sprintf("instruction %q changes condition codes but clobbers does not include cc or flags", inst.Op)})
		}
		switch {
		case strings.HasPrefix(op, "push"):
			mutatesStack = true
			state.StackTopValue, state.StackTopKnown = pushedKnownValue(state, inst.Text)
			stackDelta -= 8
			if stackMod16Known {
				stackMod16 = mod16(stackMod16 - 8)
			}
		case strings.HasPrefix(op, "pop"):
			mutatesStack = true
			state.StackTopKnown = false
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
				if param := inputRegNames[stackPointerSourceRegister(inst.Text)]; strings.Contains(param, "stack") {
					writesSPFromOwnedInput = true
				}
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
				delete(state.LiveRegs, reg)
				delete(state.KnownUInt, reg)
			}
		case op == "jmp" || op == "jmpq":
			if writesSegment && state.FS == "" && !requireSet["x86_64.segment.state.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "segment-transfer-state-unknown", File: path, Line: inst.Line, Message: "control transfer after writing fs/gs requires an explicit machine-state assertion such as state fs: host or state fs: guest"})
			}
			if target := directControlTarget(op, inst.Text); target != "" {
				if contract, ok := labelContracts[target]; ok {
					state = withStackMod16(state, stackMod16, stackMod16Known)
					issues = append(issues, checkLabelPreconditions(path, inst.Line, "jmp", target, contract.Preconditions, state)...)
				}
				if !definedLabels[target] {
					usesJmp = true
				}
			} else {
				usesJmp = true
			}
		case op == "ret" || op == "retq":
			if writesSegment && state.FS == "" && !requireSet["x86_64.segment.state.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "segment-transfer-state-unknown", File: path, Line: inst.Line, Message: "return after writing fs/gs requires an explicit machine-state assertion such as state fs: host or state fs: guest"})
			}
			if state.StackTopKnown && state.StackTopValue > 0 && state.StackTopValue < 0x10000 && !requireSet["control.tiny_target.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "tiny-return-target", File: path, Line: inst.Line, Message: fmt.Sprintf("ret target is known tiny value 0x%x; require control.tiny_target.unchecked only for intentional sentinels", state.StackTopValue)})
			}
			if state.StackTopKnown && isNonCanonicalX86Address(state.StackTopValue) && !requireSet["control.poison_target.unchecked"] {
				issues = append(issues, Issue{Severity: "error", Code: "poison-return-target", File: path, Line: inst.Line, Message: fmt.Sprintf("ret target is known non-canonical poison-like value 0x%x; require control.poison_target.unchecked only for intentional tests", state.StackTopValue)})
			}
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
			if target := directControlTarget(op, inst.Text); target != "" {
				if contract, ok := labelContracts[target]; ok {
					state = withStackMod16(state, stackMod16, stackMod16Known)
					issues = append(issues, checkLabelPreconditions(path, inst.Line, op, target, contract.Preconditions, state)...)
				}
			}
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
		for _, written := range writtenRegisters(inst.Text) {
			delete(clobberedByCall, canonicalX86GPR(written))
		}
	}
	issues = append(issues, verifyABI(path, fn)...)
	issues = append(issues, verifyContractTokens(path, fn)...)
	issues = append(issues, verifySignatureTypes(path, fn)...)
	issues = append(issues, verifyMachineRoleTypes(path, fn, requireSet, controlSet)...)
	issues = append(issues, verifyBindings(path, target, fn)...)
	issues = append(issues, verifyRegisterLists(path, target, fn)...)
	issues = append(issues, verifyDuplicateContractAtoms(path, fn)...)
	issues = append(issues, verifyEntryFacts(path, fn)...)
	issues = append(issues, verifyLabelContracts(path, fn, definedLabels)...)
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
	if !returnsVoid && returnReg != "" && !returnRegWritten && !requireSet["return.register.preinitialized"] {
		issues = append(issues, Issue{Severity: "error", Code: "return-register-not-written", File: path, Line: fn.Line, Message: fmt.Sprintf("non-void EASM export declares ret = %s but the body does not write it", returnReg)})
	}
	if !requireSet["input.unused"] {
		for reg := range inputRegs {
			if !inputRegRead[reg] {
				issues = append(issues, Issue{Severity: "error", Code: "input-register-unused", File: path, Line: fn.Line, Message: fmt.Sprintf("input register %s is declared but not read before being overwritten or returning", reg)})
			}
		}
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
	if writesSPFromOwnedInput && !stackSet["owns"] {
		issues = append(issues, Issue{Severity: "error", Code: "stack-switch-without-ownership-contract", File: path, Line: fn.Line, Message: "writing rsp from a stack-like input requires stack: owns"})
	}
	if controlSet["returns"] && writesSegment && !requireSet["x86_64.segment.restore"] && !requireSet["x86_64.segment.persistent"] {
		issues = append(issues, Issue{Severity: "error", Code: "segment-register-return-without-lifetime-contract", File: path, Line: fn.Line, Message: "returning after writing fs/gs requires x86_64.segment.restore or x86_64.segment.persistent intent"})
	}
	if stackSet["synthetic"] && usesCall && !usesJmp {
		issues = append(issues, Issue{Severity: "error", Code: "guest-entry-call-mangles-stack", File: path, Line: fn.Line, Message: "synthetic stack handoff must tail jump instead of call"})
	}
	if controlSet["noreturn"] && usesRet {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-can-return", File: path, Line: fn.Line, Message: "noreturn function contains ret"})
	}
	if controlSet["noreturn"] && !terminalOpIs(fn.Instructions, "jmp", "jmpq", "trap") {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-missing-terminal", File: path, Line: fn.Line, Message: "noreturn function must end in jmp or trap"})
	}
	if controlSet["noreturn"] && terminalOpIs(fn.Instructions, "jmp", "jmpq") && !controlSet["tail_jumps"] {
		issues = append(issues, Issue{Severity: "error", Code: "noreturn-jump-without-tail-contract", File: path, Line: fn.Line, Message: "noreturn jmp requires tail_jumps control contract"})
	}
	if controlSet["returns"] && usesJmp && !controlSet["tail_jumps"] {
		issues = append(issues, Issue{Severity: "error", Code: "returning-unqualified-jump", File: path, Line: fn.Line, Message: "returning function contains jmp without tail_jumps control contract"})
	}
	if controlSet["returns"] && !usesRet {
		issues = append(issues, Issue{Severity: "error", Code: "returns-missing-ret", File: path, Line: fn.Line, Message: "returning function must contain ret"})
	}
	if controlSet["returns"] && usesRet && !terminalOpIs(fn.Instructions, "ret", "retq") {
		issues = append(issues, Issue{Severity: "error", Code: "return-not-terminal", File: path, Line: fn.Line, Message: "returning function must end with ret"})
	}
	if controlSet["returns"] && hasCallImmediatelyBeforeRet(fn.Instructions) && !requireSet["call.return_address_choreography.unchecked"] {
		issues = append(issues, Issue{Severity: "error", Code: "call-immediately-before-ret", File: path, Line: fn.Line, Message: "call followed immediately by ret requires explicit call.return_address_choreography.unchecked intent"})
	}
	if controlSet["tail_jumps"] && clobberSet["rbp"] && !requireSet["frame_pointer.handoff.unchecked"] {
		issues = append(issues, Issue{Severity: "error", Code: "tail-jump-frame-pointer-clobber", File: path, Line: fn.Line, Message: "tail-jump handoff clobbers rbp; preserve the incoming frame chain or require frame_pointer.handoff.unchecked after ABI proof"})
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

func verifyMachineRoleTypes(path string, fn *Function, requireSet map[string]bool, controlSet map[string]bool) []Issue {
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
			}
			updateMemoryBaseProvenance(regProvenance, inst.Text)
		}
	}
	issues = append(issues, checkFrameConditions(path, fn, paramTypes)...)
	return issues
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
		if (prev == "call" || prev == "callq") && (cur == "ret" || cur == "retq") {
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

func returnOutputRegister(outputs []string) string {
	for _, output := range outputs {
		if bindingName(output) == "ret" {
			return canonicalX86GPR(registerAfterEquals(output))
		}
	}
	return ""
}

func inputRegisterSet(inputs []string) map[string]bool {
	out := map[string]bool{}
	for _, input := range inputs {
		if reg := registerAfterEquals(input); reg != "" {
			out[canonicalX86GPR(reg)] = true
		}
	}
	return out
}

func inputRegisterNameSet(inputs []string) map[string]string {
	out := map[string]string{}
	for _, input := range inputs {
		name := bindingName(input)
		if reg := registerAfterEquals(input); reg != "" && name != "" {
			out[canonicalX86GPR(reg)] = name
		}
	}
	return out
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
	name = strings.TrimSpace(name)
	switch strings.TrimSpace(name) {
	case "void", "bool", "char", "int", "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64":
		return true
	default:
		return isEASMRoleType(name) || isAddressSpaceCarrierType(name)
	}
}

func isEASMRoleType(name string) bool {
	name = strings.TrimSpace(name)
	switch name {
	case "GuestEntryPoint", "GuestCallable", "GuestThreadEntry", "GuestThreadArg", "GuestThreadResult", "GuestPC",
		"HostCallable", "NativeCallable", "ExitFunction",
		"GuestStackTop", "GuestFsSelector", "HostFsSelector", "GuestGsSelector", "HostGsSelector",
		"PublishedExecutableAddr", "WritableExecutableAddr",
		"HostStackPointer", "SegmentSelfPointer", "HostThreadId", "SignalContextPtr", "MachineContextPtr":
		return true
	default:
		return false
	}
}

func isAddressSpaceCarrierType(name string) bool {
	name = strings.TrimSpace(name)
	for _, prefix := range []string{"GuestVAddr[", "HostPtr[", "NativeMappedGuestPtr["} {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, "]") && len(name) > len(prefix)+1 {
			return true
		}
	}
	return false
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
	for _, token := range contractFields(fn.Requires) {
		if !allowedRequireToken(token) {
			issues = append(issues, Issue{Severity: "error", Code: "unknown-require-capability", File: path, Line: fn.Line, Message: fmt.Sprintf("unknown requires capability %s", token)})
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
	case "unchanged", "aligned", "16", "synthetic", "switches", "owns", "noreturn", "probed":
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

func allowedRequireToken(token string) bool {
	switch token {
	case "aarch64.cntvct",
		"aarch64.platform_register.x18",
		"aarch64.yield",
		"callee_saved.preservation.unchecked",
		"call.caller_saved_liveness.unchecked",
		"call.return_address_choreography.unchecked",
		"compare.signed",
		"compare.unsigned",
		"control.direct",
		"control.indirect",
		"control.poison_target.unchecked",
		"control.target.untyped",
		"control.tiny_target.unchecked",
		"debug.trap",
		"fixed_address",
		"frame_pointer.handoff.unchecked",
		"input.unused",
		"immediate.truncation",
		"memory.base.untyped",
		"operand_size.inferred",
		"pic",
		"relocation.symbol",
		"return.register.preinitialized",
		"riscv.reserved_registers",
		"stack.call_alignment.unchecked",
		"stack.entry_pop.unchecked",
		"stack.owner.untyped",
		"x86_64.atomic.rmw",
		"x86_64.cpuid",
		"x86_64.fpu_control",
		"x86_64.legacy_high_byte",
		"x86_64.rdtsc",
		"x86_64.rdtsc.unordered",
		"x86_64.segment",
		"x86_64.segment.fs",
		"x86_64.segment.gs",
		"x86_64.segment.persistent",
		"x86_64.segment.restore",
		"x86_64.segment.selector.untyped",
		"x86_64.segment.state.unchecked",
		"x86_64.segment.write",
		"x86_64.simd_state",
		"x86_64.sse.lfence",
		"x86_64.sse.pause":
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
		if clobber == "memory" || clobber == "memory.read" || clobber == "memory.write" || clobber == "cc" || clobber == "flags" {
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
		{name: "facts", values: fn.Facts},
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

func verifyEntryFacts(path string, fn *Function) []Issue {
	var issues []Issue
	for _, fact := range fn.Facts {
		if !supportedMachineFact(fact) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-entry-fact", File: path, Line: fn.Line, Message: fmt.Sprintf("unsupported EASM entry fact %q", fact)})
		}
	}
	for _, inst := range fn.Instructions {
		if inst.Pseudo && normalizeOp(inst.Op) == "state" && !supportedMachineFact(inst.Text) {
			issues = append(issues, Issue{Severity: "error", Code: "unsupported-state-assertion", File: path, Line: inst.Line, Message: fmt.Sprintf("unsupported EASM state assertion %q", inst.Text)})
		}
	}
	return issues
}

func verifyLabelContracts(path string, fn *Function, definedLabels map[string]bool) []Issue {
	var issues []Issue
	seen := map[string]int{}
	for _, contract := range fn.Labels {
		if contract.Name == "" || !isIdentifierLike(contract.Name) {
			issues = append(issues, Issue{Severity: "error", Code: "invalid-label-contract", File: path, Line: contract.Line, Message: "label contract must name an internal assembly label"})
			continue
		}
		if firstLine, exists := seen[contract.Name]; exists {
			issues = append(issues, Issue{Severity: "error", Code: "duplicate-label-contract", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s duplicates earlier contract on line %d", contract.Name, firstLine)})
			continue
		}
		seen[contract.Name] = contract.Line
		if _, ok := definedLabels[contract.Name]; !ok {
			issues = append(issues, Issue{Severity: "error", Code: "label-contract-without-label", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s has no matching body label", contract.Name)})
		}
		if len(contract.Preconditions) == 0 {
			issues = append(issues, Issue{Severity: "error", Code: "empty-label-precondition", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s must require at least one machine-state precondition", contract.Name)})
		}
		for _, pre := range contract.Preconditions {
			if supportedMachineFact(pre) {
				continue
			}
			reg := canonicalRegisterPrecondition(pre)
			if reg == "" || !isX86GPR(reg) {
				issues = append(issues, Issue{Severity: "error", Code: "unsupported-label-precondition", File: path, Line: contract.Line, Message: fmt.Sprintf("label contract %s uses unsupported machine-state precondition %q", contract.Name, pre)})
			}
		}
	}
	return issues
}

func labelContractMap(contracts []LabelContract) map[string]LabelContract {
	out := map[string]LabelContract{}
	for _, contract := range contracts {
		if contract.Name != "" {
			out[contract.Name] = contract
		}
	}
	return out
}

func bodyLabelDefinitions(instructions []Instruction) map[string]bool {
	out := map[string]bool{}
	for _, inst := range instructions {
		if inst.Label != "" {
			out[inst.Label] = true
		}
	}
	return out
}

func canonicalRegisterPreconditionSet(preconditions []string) map[string]bool {
	out := map[string]bool{}
	for _, pre := range preconditions {
		if reg := canonicalRegisterPrecondition(pre); reg != "" {
			out[reg] = true
		}
	}
	return out
}

func applyMachineFactPreconditions(state machineFactState, preconditions []string) machineFactState {
	out := machineFactState{LiveRegs: map[string]bool{}, KnownUInt: map[string]uint64{}, FS: state.FS, StackMod16: state.StackMod16, StackMod16Known: state.StackMod16Known}
	for reg := range state.LiveRegs {
		out.LiveRegs[reg] = true
	}
	for reg, value := range state.KnownUInt {
		out.KnownUInt[reg] = value
	}
	for _, pre := range preconditions {
		if expected, ok := segmentFactPrecondition(pre); ok {
			out.FS = expected
			continue
		}
		if mod, ok := stackMod16FactPrecondition(pre); ok {
			out.StackMod16 = mod
			out.StackMod16Known = true
			continue
		}
		if reg := canonicalRegisterPrecondition(pre); reg != "" && isX86GPR(reg) {
			out.LiveRegs[reg] = true
		}
	}
	return out
}

func applyMachineStateAssertion(state machineFactState, assertion string) machineFactState {
	if state.LiveRegs == nil {
		state.LiveRegs = map[string]bool{}
	}
	if state.KnownUInt == nil {
		state.KnownUInt = map[string]uint64{}
	}
	if expected, ok := segmentFactPrecondition(assertion); ok {
		state.FS = expected
	}
	if mod, ok := stackMod16FactPrecondition(assertion); ok {
		state.StackMod16 = mod
		state.StackMod16Known = true
	}
	return state
}

func supportedMachineFact(fact string) bool {
	if _, ok := segmentFactPrecondition(fact); ok {
		return true
	}
	_, ok := stackMod16FactPrecondition(fact)
	return ok
}

func withStackMod16(state machineFactState, mod int, known bool) machineFactState {
	state.StackMod16 = mod
	state.StackMod16Known = known
	return state
}

func segmentFactPrecondition(pre string) (string, bool) {
	pre = strings.ToLower(strings.TrimSpace(pre))
	pre = strings.ReplaceAll(pre, " ", "")
	pre = strings.TrimPrefix(pre, "%")
	for _, sep := range []string{":", "="} {
		if before, after, ok := strings.Cut(pre, sep); ok {
			before = strings.TrimPrefix(before, "%")
			if before == "fs" && (after == "host" || after == "guest") {
				return after, true
			}
		}
	}
	return "", false
}

func stackMod16FactPrecondition(pre string) (int, bool) {
	pre = strings.ToLower(strings.TrimSpace(pre))
	pre = strings.ReplaceAll(pre, " ", "")
	pre = strings.TrimPrefix(pre, "%")
	if before, after, ok := strings.Cut(pre, ":"); ok {
		before = strings.TrimPrefix(before, "%")
		if before != "rsp" && before != "esp" && before != "sp" {
			return 0, false
		}
		switch after {
		case "aligned16", "call_aligned", "mod16=0":
			return 0, true
		case "entry_aligned", "mod16=8":
			return 8, true
		}
	}
	return 0, false
}

func canonicalRegisterPrecondition(pre string) string {
	pre = strings.TrimSpace(pre)
	if pre == "" {
		return ""
	}
	if before, _, ok := strings.Cut(pre, ":"); ok {
		pre = before
	}
	if before, _, ok := strings.Cut(pre, "="); ok {
		pre = before
	}
	pre = strings.TrimPrefix(strings.TrimSpace(pre), "%")
	return canonicalX86GPR(pre)
}

func checkLabelPreconditions(path string, line int, transferKind string, label string, preconditions []string, state machineFactState) []Issue {
	var issues []Issue
	for _, pre := range preconditions {
		if expected, ok := segmentFactPrecondition(pre); ok {
			if state.FS != expected {
				actual := defaultString(state.FS, "unknown")
				issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s requires fs:%s but current fs state is %s", transferKind, label, expected, actual)})
			}
			continue
		}
		if expected, ok := stackMod16FactPrecondition(pre); ok {
			if !state.StackMod16Known {
				issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s requires rsp mod 16 = %d but current stack alignment is unknown", transferKind, label, expected)})
			} else if state.StackMod16 != expected {
				issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s requires rsp mod 16 = %d but current rsp mod 16 = %d", transferKind, label, expected, state.StackMod16)})
			}
			continue
		}
		reg := canonicalRegisterPrecondition(pre)
		if reg == "" {
			continue
		}
		if !state.LiveRegs[reg] {
			issues = append(issues, Issue{Severity: "error", Code: "label-precondition-unsatisfied", File: path, Line: line, Message: fmt.Sprintf("%s to label %s does not satisfy required live register %s", transferKind, label, reg)})
		}
	}
	return issues
}

func directControlTarget(op string, text string) string {
	if op != "jmp" && op != "jmpq" && !isConditionalJump(op) {
		return ""
	}
	operands := splitInstructionOperands(text)
	if len(operands) == 0 {
		fields := strings.Fields(text)
		if len(fields) < 2 {
			return ""
		}
		operands = []string{fields[len(fields)-1]}
	}
	target := strings.TrimSpace(operands[len(operands)-1])
	target = strings.TrimPrefix(target, "*")
	target = strings.TrimSuffix(target, ";")
	if !isIdentifierLike(target) {
		return ""
	}
	return target
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

func stackPointerSourceRegister(text string) string {
	parts := splitInstructionOperands(text)
	if len(parts) < 2 || !writesStackPointer(text) {
		return ""
	}
	src := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(parts[0])), "%")
	if isRegisterName(src) {
		return canonicalX86GPR(src)
	}
	return ""
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

// implicitUses returns the registers an instruction reads WITHOUT them appearing as an
// explicit operand -- e.g. cpuid consumes the leaf in eax and the subleaf in ecx. Such
// reads are invisible in the assembly text, so unless the validator knows about them it
// cannot tell whether the register holds a value the function established (a declared
// input or an earlier write) or an indeterminate value the caller happened to leave. The
// table is cross-checked against LLVM MC's implicit_uses in easm_mc_effects_test.go so it
// can never silently miss a read. Returned names are canonical 64-bit forms.
func implicitUses(op string) []string {
	switch op {
	case "cpuid":
		return []string{"rax", "rcx"}
	default:
		return nil
	}
}

// implicitResultDefines reports whether an instruction's implicit clobbers are defined
// results it writes (cpuid, rdtsc) rather than registers it trashes to an indeterminate
// value (a call's caller-saved set). Result writes establish the registers for later
// reads; trashing leaves them unreadable until re-established.
func implicitResultDefines(op string) bool {
	switch op {
	case "rdtsc", "cpuid":
		return true
	}
	return false
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
	case "mov", "movq", "movl", "movw", "movb", "lea", "pop", "popq":
		return true
	case "xor", "xorq":
		parts := splitInstructionOperands(text)
		return len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	default:
		return isMoveExtendOp(normalizeOp(fields[0]))
	}
}

func isMoveExtendOp(op string) bool {
	switch op {
	case "movsx", "movsxd", "movsbw", "movsbl", "movsbq", "movswl", "movswq", "movslq",
		"movzx", "movzbw", "movzbl", "movzbq", "movzwl", "movzwq":
		return true
	default:
		return false
	}
}

// isSelfZeroingIdiom reports whether the instruction is a recognized constant-zeroing
// idiom (xorq %r,%r / subq %r,%r) whose result is independent of the register's prior
// value. Such an instruction establishes the register (to zero) without reading it.
func isSelfZeroingIdiom(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	switch normalizeOp(fields[0]) {
	case "xor", "xorq", "xorl", "xorw", "xorb", "sub", "subq", "subl", "subw", "subb":
		parts := splitInstructionOperands(text)
		return len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}
	return false
}

// registersReadBy returns the canonical GPRs whose value the instruction consumes: every
// source-operand register, plus the base/index registers of any memory operand (address
// computation reads them, even when that operand is the destination). It excludes a
// destination register that is purely written (mov/lea/pop) and the operands of a
// self-zeroing idiom, neither of which depends on the prior value. This is the read set the
// validator requires to be established -- a declared input, written earlier, or preserved.
func registersReadBy(text string) []string {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 || isSelfZeroingIdiom(text) {
		return nil
	}
	overwrites := instructionOverwritesDestination(text)
	seen := map[string]bool{}
	var out []string
	addToken := func(tok string) {
		reg := canonicalX86GPR(tok)
		if isX86GPR(reg) && !seen[reg] {
			seen[reg] = true
			out = append(out, reg)
		}
	}
	for i, operand := range parts {
		isMemory := strings.Contains(operand, "(")
		isDest := i == len(parts)-1
		if !isMemory && isDest && overwrites {
			continue // destination written outright; its prior value is not read
		}
		for _, tok := range registerTokens(operand) {
			addToken(tok)
		}
	}
	return out
}

func writtenRegister(text string) string {
	regs := writtenRegisters(text)
	if len(regs) == 0 {
		return ""
	}
	return regs[0]
}

func writtenRegisters(text string) []string {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return nil
	}
	fields := strings.Fields(text)
	op := ""
	if len(fields) > 0 {
		op = normalizeOp(fields[0])
	}
	if strings.HasPrefix(op, "push") || op == "call" || op == "callq" || op == "jmp" || op == "jmpq" || op == "ret" || op == "retq" {
		return nil
	}
	if op == "cmp" || op == "cmpq" || op == "test" || op == "testq" || isConditionalJump(op) {
		return nil
	}
	if op == "xchg" || op == "xchgl" || op == "xchgq" {
		seen := map[string]bool{}
		var out []string
		for _, part := range parts {
			reg := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(part)), "%")
			if !isRegisterName(reg) {
				continue
			}
			reg = canonicalX86GPR(reg)
			if seen[reg] {
				continue
			}
			seen[reg] = true
			out = append(out, reg)
		}
		return out
	}
	dst := strings.TrimSpace(parts[len(parts)-1])
	dst = strings.TrimPrefix(strings.ToLower(dst), "%")
	if isRegisterName(dst) {
		return []string{dst}
	}
	return nil
}

func updateKnownRegisterValue(state machineFactState, text string, reg string) {
	if state.KnownUInt == nil {
		state.KnownUInt = map[string]uint64{}
	}
	delete(state.KnownUInt, reg)
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}
	op := normalizeOp(fields[0])
	parts := splitInstructionOperands(text)
	if len(parts) < 2 {
		return
	}
	if (op == "mov" || op == "movq" || op == "movl") && canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(parts[len(parts)-1]), "%")) == reg {
		if value, ok := parseImmediateLiteral(parts[0]); ok {
			state.KnownUInt[reg] = value
		}
		return
	}
	if (op == "xor" || op == "xorq") && len(parts) == 2 {
		left := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(parts[0]), "%"))
		right := canonicalX86GPR(strings.TrimPrefix(strings.TrimSpace(parts[1]), "%"))
		if left != "" && left == right && right == reg {
			state.KnownUInt[reg] = 0
		}
	}
}

func parseImmediateLiteral(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "$")
	value = strings.TrimPrefix(value, "#")
	value = strings.TrimSuffix(value, ";")
	value = strings.ReplaceAll(value, "_", "")
	if value == "" || strings.HasPrefix(value, "-") {
		return 0, false
	}
	parsed, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func pushedKnownValue(state machineFactState, text string) (uint64, bool) {
	parts := splitInstructionOperands(text)
	if len(parts) != 1 {
		return 0, false
	}
	operand := strings.TrimSpace(parts[0])
	if value, ok := parseImmediateLiteral(operand); ok {
		return value, true
	}
	reg := canonicalX86GPR(strings.TrimPrefix(operand, "%"))
	if reg == "" {
		return 0, false
	}
	value, ok := state.KnownUInt[reg]
	return value, ok
}

func isNonCanonicalX86Address(value uint64) bool {
	sign := (value >> 47) & 1
	upper := value >> 48
	if sign == 0 {
		return upper != 0
	}
	return upper != 0xffff
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
	case "esi", "si", "sil":
		return "rsi"
	case "edi", "di", "dil":
		return "rdi"
	case "esp":
		return "rsp"
	case "ebp":
		return "rbp"
	default:
		for _, base := range []string{"r8", "r9", "r10", "r11", "r12", "r13", "r14", "r15"} {
			if reg == base+"d" || reg == base+"w" || reg == base+"b" {
				return base
			}
		}
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
	if len(fields) > 0 && (isConditionalJump(normalizeOp(fields[0])) || normalizeOp(fields[0]) == "jmp" || normalizeOp(fields[0]) == "jmpq" || strings.HasPrefix(normalizeOp(fields[0]), "call")) {
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
	if op != "jmp" && op != "jmpq" && op != "call" && op != "callq" {
		return false
	}
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(parts[0]), "*")
}

func indirectControlTargetRegister(text string) string {
	parts := splitInstructionOperands(text)
	if len(parts) == 0 {
		return ""
	}
	target := strings.TrimSpace(parts[0])
	target = strings.TrimPrefix(target, "*")
	target = strings.TrimPrefix(target, "%")
	if isRegisterName(target) {
		return canonicalX86GPR(target)
	}
	return ""
}

func isDirectSymbolControlTransfer(op string, text string) bool {
	if op != "jmp" && op != "jmpq" && op != "call" && op != "callq" {
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
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	op := normalizeOp(fields[0])
	parts := splitInstructionOperands(text)
	if len(parts) == 1 {
		switch op {
		case "inc", "incq", "dec", "decq", "pop", "popq":
			return operandIsMemory(parts[0])
		case "fnstcw", "stmxcsr":
			return operandIsMemory(parts[0])
		}
		return false
	}
	if len(parts) < 2 {
		return false
	}
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

// memoryBaseRegister returns the canonical base register of a memory operand of the form
// disp(base, index, scale), or "" if it has no GPR base (a segment-relative reference or an
// absolute address). Only the base is returned -- the index register is a scalar offset, not
// a pointer, so it is not subject to pointer-provenance typing.
func memoryBaseRegister(operand string) string {
	open := strings.Index(operand, "(")
	if open < 0 {
		return ""
	}
	rel := strings.Index(operand[open:], ")")
	if rel < 0 {
		return ""
	}
	inner := operand[open+1 : open+rel]
	fields := strings.Split(inner, ",")
	base := strings.TrimPrefix(strings.TrimSpace(fields[0]), "%")
	if base == "" {
		return ""
	}
	reg := canonicalX86GPR(base)
	if !isX86GPR(reg) {
		return ""
	}
	return reg
}

// operandIsMemory reports whether a single operand denotes a memory location: a
// parenthesized base/index expression or a segment-relative reference.
func operandIsMemory(operand string) bool {
	if strings.Contains(operand, "(") && strings.Contains(operand, ")") {
		return true
	}
	return usesSegmentOverride(operand)
}

// readsMemory reports whether a two-or-more-operand instruction loads a value from memory:
// a memory source operand, or a memory destination that is read-modify-written (add/sub/...
// to memory). lea computes an address without touching memory; a pure store (mov to memory)
// writes without reading; and xchg with a memory operand both reads and writes it.
//
// Single-operand stack instructions (push/pop) are intentionally excluded: they are
// governed by the stack contract. Single-operand FPU-control memory forms still participate
// in memory direction checks: fldcw/ldmxcsr read memory, fnstcw/stmxcsr write memory.
func readsMemory(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	op := normalizeOp(fields[0])
	if op == "lea" || op == "leaq" || op == "leal" {
		return false
	}
	parts := splitInstructionOperands(text)
	if len(parts) == 1 {
		switch op {
		case "inc", "incq", "dec", "decq", "push", "pushq", "call", "callq", "jmp", "jmpq":
			return operandIsMemory(parts[0])
		case "fldcw", "ldmxcsr":
			return operandIsMemory(parts[0])
		}
		return false
	}
	if len(parts) < 2 {
		return false
	}
	if op == "xchg" || op == "xchgl" || op == "xchgq" {
		return hasMemoryOperand(text)
	}
	for i, operand := range parts {
		if !operandIsMemory(operand) {
			continue
		}
		if i != len(parts)-1 {
			return true // memory source operand -> load
		}
		if !instructionOverwritesDestination(text) {
			return true // read-modify-write of a memory destination
		}
	}
	return false
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
