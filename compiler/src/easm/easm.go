package easm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	Layouts   []Layout
}

// Layout is a typed record shape behind a pointer carrier (`HostPtr[Name]` / `GuestVAddr[Name]`).
// It lifts EASM toward typed assembly: a memory access `off(%base)` through a layout-typed carrier
// is checked to land on a declared field of matching width, rather than "some memory at an offset".
type Layout struct {
	Name   string
	Fields []LayoutField
	// Size is the layout's explicitly declared byte size from a `layout Name size N:` header.
	// 0 means no explicit size was declared; bounds are then derived from the fields via
	// LayoutSize, preserving the field-inferred behavior of layouts written without `size`.
	Size int64
	// Stride is the per-element byte size of a fixed-stride table layout from a
	// `layout Name stride N:` header (docs/108). 0 means the layout is a single-struct overlay
	// (no stride). When >0, the layout describes a packed array of records and field offsets are
	// bounded by the stride rather than the struct size; the indexed accessor `table.field[mem, i]`
	// reads entry i at base + i*Stride + field.Offset. `size` and `stride` are mutually exclusive.
	Stride int64
	Line   int
}

type LayoutField struct {
	Offset int64
	Name   string
	Type   string
	Width  int // bytes; 0 = width unknown (offset checked, width not)
	// RequiresSizeAtLeast is the field's declared minimum runtime struct size, from a
	// `requires size >= N` suffix on the field line (docs/107 increment 4). 0 means the field
	// is unconditionally present. When >0, an overlay accessor of this field must be dominated by
	// a guard establishing the struct's runtime `size >= N`, else CheckGuestOverlaySizeGuard emits
	// overlay-field-needs-size-guard. This declaratively replaces the emulator's hand-written
	// "if size < N: fall back to defaults" under-size guards.
	RequiresSizeAtLeast int64
	Line                int
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
	Reference    []Instruction // portable spec body for `lockstep` translation validation, if any
	Targets      []TargetBody  // per-arch implementations validated against Reference
	Line         int
	IsFragment   bool
	Platform     string
}

// TargetBody is one architecture-specific implementation of a routine that declares it must be
// observationally equivalent to the routine's `reference` spec body (`target <arch> lockstep
// <ref>:`). Its instruction stream is verified with the full machinery exactly like an ordinary
// body; the runtime equivalence proof against the named reference is the @lockstep oracle (see
// docs/103 stage 3c) and is not yet wired.
type TargetBody struct {
	Arch         string
	Lockstep     string
	Instructions []Instruction
	Line         int
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

var verifyFunctionStateHook func(path string, fn *Function, state machineFactState)

func easmCopyMachineFactState(state machineFactState) machineFactState {
	state.LiveRegs = easmCopyLive(state.LiveRegs)
	state.KnownUInt = easmCopyKnownUInt(state.KnownUInt)
	return state
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
	lockstepTargetRE = regexp.MustCompile(`^target\s+([A-Za-z0-9_]+)\s+lockstep\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*$`)
	layoutHeaderRE   = regexp.MustCompile(`^layout\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s+size\s+(\d+)|\s+stride\s+(\d+))?\s*:\s*$`)
	layoutFieldRE    = regexp.MustCompile(`^(\d+)\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*([A-Za-z_][A-Za-z0-9_\[\]]*)\s*(?:\s+requires\s+size\s*>=\s*(\d+))?\s*$`)
	identTokenRE     = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	protocolMethodRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*\(([^)]*)\)\s*->\s*([A-Za-z_][A-Za-z0-9_\[\]&?]*)\s*$`)
	platformForRE    = regexp.MustCompile(`\bfor\b\s+(\([^)]*\)|[^\s]+)`)
	nativeCanRE      = regexp.MustCompile(`\bcan\b\s+([^\[].*?)(?:\s+abi\b|\s+for\b|$)`)
	sectionRE        = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*)\s*:\s*(.*)$`)
	allowedOps       = map[string]bool{
		"mov": true, "movq": true, "lea": true, "push": true, "pushq": true, "pop": true, "popq": true,
		"movb": true, "movw": true, "movl": true,
		"movsx": true, "movsxd": true, "movsbw": true, "movsbl": true, "movsbq": true, "movswl": true, "movswq": true, "movslq": true,
		"movzx": true, "movzbw": true, "movzbl": true, "movzbq": true, "movzwl": true, "movzwq": true,
		"xchg": true, "xchgl": true, "xchgq": true,
		"add": true, "addq": true, "sub": true, "subq": true, "and": true, "andq": true,
		"or": true, "orq": true, "not": true, "notq": true, "neg": true, "negq": true,
		"leaq": true, "shl": true, "shlq": true, "sal": true, "salq": true, "shr": true, "shrq": true, "sar": true, "sarq": true,
		"cmp": true, "cmpq": true, "test": true, "testq": true, "inc": true, "incq": true, "dec": true, "decq": true,
		"xor": true, "xorq": true, "call": true, "callq": true, "jmp": true, "jmpq": true, "ret": true, "retq": true,
		"cpuid": true, "cld": true, "std": true,
		"lfence": true, "rdtsc": true, "pause": true, "yield": true, "mrs": true, "isb": true,
		"fldcw": true, "fnstcw": true, "stmxcsr": true, "ldmxcsr": true, "emms": true,
		"vzeroall": true, "trap": true,
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
	var currentLayout *Layout
	var section string
	// bodySink, when non-nil, redirects body instructions into a `reference:` or `target … lockstep`
	// sub-block instead of the function's primary body. Reset to nil whenever a new routine begins.
	var bodySink *[]Instruction
	scanner := bufio.NewScanner(strings.NewReader(src))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := stripComment(strings.TrimSpace(raw))
		if line == "" {
			continue
		}
		// Any declaration header (a new routine, fragment, or `target … lockstep` block) closes the
		// previous body sink; the reference/target detection below re-establishes it in the same pass.
		if isHeaderLine(line) {
			bodySink = nil
		}
		if currentProto != nil {
			if m := protocolMethodRE.FindStringSubmatch(line); m != nil {
				currentProto.Methods = append(currentProto.Methods, protocolMethod{Name: m[1], Arity: len(splitCSV(m[2])), Return: m[3]})
				continue
			}
			currentProto = nil
		}
		if currentLayout != nil {
			if m := layoutFieldRE.FindStringSubmatch(line); m != nil {
				offset, _ := strconv.ParseInt(m[1], 10, 64)
				width, _ := layoutTypeWidth(m[3])
				var reqSize int64
				if m[4] != "" {
					reqSize, _ = strconv.ParseInt(m[4], 10, 64)
				}
				currentLayout.Fields = append(currentLayout.Fields, LayoutField{Offset: offset, Name: m[2], Type: m[3], Width: width, RequiresSizeAtLeast: reqSize, Line: lineNo})
				continue
			}
			currentLayout = nil
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
			case layoutHeaderRE.MatchString(line):
				m := layoutHeaderRE.FindStringSubmatch(line)
				module.Layouts = append(module.Layouts, Layout{Name: m[1], Size: parseLayoutSize(m[2]), Stride: parseLayoutSize(m[3]), Line: lineNo})
				currentLayout = &module.Layouts[len(module.Layouts)-1]
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
		if layoutHeaderRE.MatchString(line) {
			m := layoutHeaderRE.FindStringSubmatch(line)
			module.Layouts = append(module.Layouts, Layout{Name: m[1], Size: parseLayoutSize(m[2]), Stride: parseLayoutSize(m[3]), Line: lineNo})
			currentLayout = &module.Layouts[len(module.Layouts)-1]
			current = nil
			section = ""
			continue
		}
		// `reference:` and `target <arch> lockstep <ref>:` open body sub-blocks: subsequent
		// instructions flow into the spec or the per-arch implementation rather than the primary body.
		if line == "reference:" {
			bodySink = &current.Reference
			section = "body"
			continue
		}
		if m := lockstepTargetRE.FindStringSubmatch(line); m != nil {
			current.Targets = append(current.Targets, TargetBody{Arch: m[1], Lockstep: m[2], Line: lineNo})
			bodySink = &current.Targets[len(current.Targets)-1].Instructions
			section = "body"
			continue
		}
		if matches := sectionRE.FindStringSubmatch(line); matches != nil && isSection(matches[1]) {
			section = strings.ToLower(matches[1])
			if rest := strings.TrimSpace(matches[2]); rest != "" {
				if section == "body" {
					addBodyOrSink(current, bodySink, rest, lineNo)
				} else {
					addSectionValue(current, section, rest, lineNo)
				}
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
		addBodyOrSink(current, bodySink, lineWithIndent(raw), lineNo)
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
		protocolHeaderRE.MatchString(line) || templateHeaderRE.MatchString(line) ||
		layoutHeaderRE.MatchString(line)
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
