package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type nativeABILintSeverity string

const (
	nativeABILintInfo    nativeABILintSeverity = "info"
	nativeABILintWarning nativeABILintSeverity = "warning"
	nativeABILintError   nativeABILintSeverity = "error"
)

type nativeABILintIssue struct {
	Severity nativeABILintSeverity `json:"severity"`
	Code     string                `json:"code"`
	File     string                `json:"file"`
	Line     int                   `json:"line,omitempty"`
	Message  string                `json:"message"`
}

type nativeABILintOptions struct {
	StrictContracts bool
}

type nativeABILintReport struct {
	Project      string               `json:"project"`
	Target       string               `json:"target"`
	Entry        string               `json:"entry"`
	Emit         string               `json:"emit"`
	RunEmit      string               `json:"runEmit"`
	TargetTriple string               `json:"targetTriple,omitempty"`
	Foreign      []string             `json:"foreign,omitempty"`
	Scanned      []string             `json:"scanned,omitempty"`
	LinkFlags    []string             `json:"linkFlags,omitempty"`
	Contracts    []string             `json:"contracts,omitempty"`
	Issues       []nativeABILintIssue `json:"issues"`
}

var (
	nativeAsmTokenRE         = regexp.MustCompile(`\b(__asm__|asm)\b`)
	nativeGuestEntryNameRE   = regexp.MustCompile(`(?i)(RunMainEntry|guest_exec|GuestExec|entry_trampoline|call_main_entry|jump_main_entry)`)
	nativeAsmPositionalOpRE  = regexp.MustCompile(`%[0-9]`)
	nativeAsmArgRegisterRE   = regexp.MustCompile(`%%r(di|si|dx|cx|8|9)\b`)
	nativeAsmStackRegisterRE = regexp.MustCompile(`\b(pushq|popq)\b|\b(andq|subq|addq|lea)\b[^\n]*%%rsp\b`)
	nativeAsmCallIndirectRE  = regexp.MustCompile(`\bcall\s+\*`)
	nativeAsmJumpIndirectRE  = regexp.MustCompile(`\bjmp\s+\*`)
	nativeNoreturnRE         = regexp.MustCompile(`(?i)(noreturn|__builtin_unreachable|\[\[noreturn\]\])`)
	nativeMemoryClobberRE    = regexp.MustCompile(`"memory"`)
	nativeNamedScratchRE     = regexp.MustCompile(`%%r(9|10|11)\b`)
	nativeQuotedIncludeRE    = regexp.MustCompile(`^\s*#\s*include\s+"([^"]+)"`)
	nativeABIContractRE      = regexp.MustCompile(`ELISA_ABI_CONTRACT\s+([^\n*/]+)`)
)

func buildNativeABILintReport(target *resolvedProjectTarget, options nativeABILintOptions) (*nativeABILintReport, error) {
	if target == nil {
		return nil, fmt.Errorf("resolved project target is nil")
	}
	report := &nativeABILintReport{
		Project:      target.project.filePath,
		Target:       target.name,
		Entry:        target.entryPath,
		Emit:         target.emit,
		RunEmit:      target.runEmit,
		TargetTriple: target.targetTriple,
		Foreign:      append([]string(nil), target.foreignFiles...),
		LinkFlags:    append([]string(nil), target.linkFlags...),
	}
	sort.Strings(report.Foreign)
	seen := map[string]bool{}
	for _, foreign := range report.Foreign {
		issues, contracts, err := lintNativeABISourceRecursive(foreign, seen)
		if err != nil {
			report.Issues = append(report.Issues, nativeABILintIssue{
				Severity: nativeABILintError,
				Code:     "native-source-read-failed",
				File:     foreign,
				Message:  err.Error(),
			})
			continue
		}
		report.Issues = append(report.Issues, issues...)
		report.Contracts = append(report.Contracts, contracts...)
	}
	for scanned := range seen {
		report.Scanned = append(report.Scanned, scanned)
	}
	sort.Strings(report.Scanned)
	report.Contracts = dedupeStrings(report.Contracts)
	sort.Strings(report.Contracts)
	if options.StrictContracts {
		report.Issues = append(report.Issues, lintNativeABIStrictContracts(report)...)
	}
	if strings.TrimSpace(report.TargetTriple) == "" && len(report.Foreign) != 0 {
		report.Issues = append(report.Issues, nativeABILintIssue{
			Severity: nativeABILintInfo,
			Code:     "target-triple-defaulted",
			File:     report.Project,
			Message:  "target does not set an explicit target triple; native code will use the compiler default host triple",
		})
	}
	return report, nil
}

func lintNativeABISourceRecursive(path string, seen map[string]bool) ([]nativeABILintIssue, []string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	if seen[abs] {
		return nil, nil, nil
	}
	seen[abs] = true
	issues, contracts, err := lintNativeABISource(abs)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, nil, err
	}
	baseDir := filepath.Dir(abs)
	for _, line := range strings.Split(string(data), "\n") {
		match := nativeQuotedIncludeRE.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		includePath := filepath.Clean(filepath.Join(baseDir, match[1]))
		ext := strings.ToLower(filepath.Ext(includePath))
		if ext != ".c" && ext != ".cc" && ext != ".cpp" && ext != ".cxx" && ext != ".h" && ext != ".hpp" && ext != ".hh" {
			continue
		}
		if _, statErr := os.Stat(includePath); statErr != nil {
			continue
		}
		childIssues, childContracts, childErr := lintNativeABISourceRecursive(includePath, seen)
		if childErr != nil {
			issues = append(issues, nativeABILintIssue{
				Severity: nativeABILintWarning,
				Code:     "native-include-read-failed",
				File:     includePath,
				Message:  childErr.Error(),
			})
			continue
		}
		issues = append(issues, childIssues...)
		contracts = append(contracts, childContracts...)
	}
	return issues, contracts, nil
}

func lintNativeABISource(path string) ([]nativeABILintIssue, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	issues := make([]nativeABILintIssue, 0)
	contracts := collectNativeABIContracts(text)
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".c" && ext != ".cc" && ext != ".cpp" && ext != ".cxx" && ext != ".h" && ext != ".hpp" && ext != ".hh" {
		return issues, contracts, nil
	}
	for i, line := range lines {
		if nativeAsmTokenRE.MatchString(line) {
			block, endLine := collectNativeAsmBlock(lines, i)
			contextStart := i - 6
			if contextStart < 0 {
				contextStart = 0
			}
			contextBlock := strings.Join(lines[contextStart:i], "\n") + "\n" + block
			issues = append(issues, lintNativeAsmBlock(path, i+1, endLine+1, contextBlock)...)
		}
	}
	return issues, contracts, nil
}

func collectNativeAsmBlock(lines []string, start int) (string, int) {
	var b strings.Builder
	parenDepth := 0
	sawParen := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		b.WriteString(line)
		b.WriteByte('\n')
		for _, r := range line {
			if r == '(' {
				parenDepth++
				sawParen = true
			} else if r == ')' && parenDepth > 0 {
				parenDepth--
			}
		}
		if sawParen && parenDepth == 0 && strings.Contains(line, ";") {
			return b.String(), i
		}
		if i-start >= 80 {
			return b.String(), i
		}
	}
	return b.String(), len(lines) - 1
}

func collectNativeABIContracts(text string) []string {
	matches := nativeABIContractRE.FindAllStringSubmatch(text, -1)
	contracts := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			contracts = append(contracts, strings.TrimSpace(match[1]))
		}
	}
	return contracts
}

func lintNativeABIStrictContracts(report *nativeABILintReport) []nativeABILintIssue {
	issues := []nativeABILintIssue{}
	hasGuestEntry := false
	for _, contract := range report.Contracts {
		if strings.Contains(contract, "guest_entry") {
			hasGuestEntry = true
		}
	}
	for _, scanned := range report.Scanned {
		if nativeGuestEntryNameRE.MatchString(scanned) && !hasGuestEntry {
			issues = append(issues, nativeABILintIssue{Severity: nativeABILintError, Code: "missing-guest-entry-abi-contract", File: scanned, Message: "strict ABI contracts require ELISA_ABI_CONTRACT guest_entry for guest execution native runtime files"})
		}
	}
	if hasGuestEntry && !strings.Contains(report.TargetTriple, "x86_64") {
		issues = append(issues, nativeABILintIssue{Severity: nativeABILintError, Code: "guest-entry-target-not-x86_64", File: report.Project, Message: "guest_entry ABI contract requires an explicit x86_64 target triple for real guest execution"})
	}
	return issues
}

func lintNativeAsmBlock(file string, startLine int, endLine int, block string) []nativeABILintIssue {
	issues := make([]nativeABILintIssue, 0)
	guestish := nativeGuestEntryNameRE.MatchString(block)
	touchesArgs := nativeAsmArgRegisterRE.MatchString(block)
	touchesStack := nativeAsmStackRegisterRE.MatchString(block)
	usesPositional := nativeAsmPositionalOpRE.MatchString(block)
	usesCall := nativeAsmCallIndirectRE.MatchString(block)
	usesJump := nativeAsmJumpIndirectRE.MatchString(block)
	noreturnish := nativeNoreturnRE.MatchString(block)
	hasMemoryClobber := nativeMemoryClobberRE.MatchString(block)
	hasScratchParking := nativeNamedScratchRE.MatchString(block)

	if usesPositional && (touchesArgs || touchesStack) && !nativeABILintAllowed(block, "inline-asm-positional-abi-operands") {
		issues = append(issues, nativeABILintIssue{
			Severity: nativeABILintWarning,
			Code:     "inline-asm-positional-abi-operands",
			File:     file,
			Line:     startLine,
			Message:  "inline asm touches ABI stack/argument registers while using positional operands like %0/%1; prefer named operands parked in scratch registers before clobbering rdi/rsi/rsp",
		})
	}
	if touchesStack && !hasMemoryClobber && !nativeABILintAllowed(block, "inline-asm-stack-without-memory-clobber") {
		issues = append(issues, nativeABILintIssue{
			Severity: nativeABILintWarning,
			Code:     "inline-asm-stack-without-memory-clobber",
			File:     file,
			Line:     startLine,
			Message:  "inline asm mutates stack state but does not declare a memory clobber; this can hide ABI-sensitive stack setup from the optimizer",
		})
	}
	if guestish && usesCall && touchesStack && !nativeABILintAllowed(block, "guest-entry-call-mangles-stack") {
		severity := nativeABILintError
		message := "guest-entry inline asm prepares a synthetic process stack but uses call *; C++ shadPS4 uses a no-return jmp for real guest entry so call will push a host return address"
		if !noreturnish {
			message += " and this block is not marked noreturn"
		}
		issues = append(issues, nativeABILintIssue{
			Severity: severity,
			Code:     "guest-entry-call-mangles-stack",
			File:     file,
			Line:     startLine,
			Message:  message,
		})
	}
	if guestish && usesJump && touchesStack && !noreturnish && !nativeABILintAllowed(block, "guest-entry-jump-not-noreturn") {
		issues = append(issues, nativeABILintIssue{
			Severity: nativeABILintWarning,
			Code:     "guest-entry-jump-not-noreturn",
			File:     file,
			Line:     startLine,
			Message:  "guest-entry inline asm jumps into guest code but the surrounding helper does not look noreturn; add a noreturn attribute or builtin unreachable",
		})
	}
	if guestish && touchesArgs && touchesStack && !hasScratchParking && !nativeABILintAllowed(block, "guest-entry-no-scratch-register-parking") {
		issues = append(issues, nativeABILintIssue{
			Severity: nativeABILintWarning,
			Code:     "guest-entry-no-scratch-register-parking",
			File:     file,
			Line:     startLine,
			Message:  "guest-entry inline asm sets ABI argument registers and stack without obvious r9/r10/r11 scratch parking; operand allocation can alias registers that the asm later overwrites",
		})
	}
	_ = endLine
	return issues
}

func nativeABILintAllowed(block string, code string) bool {
	return strings.Contains(block, "ELISA_ABI_LINT_ALLOW("+code+")") || strings.Contains(block, "ELISA_ABI_LINT_ALLOW(all)")
}

func formatNativeABILintReport(report *nativeABILintReport, jsonOutput bool) (string, error) {
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
	fmt.Fprintf(&out, "Project: %s\n", report.Project)
	fmt.Fprintf(&out, "Target: %s\n", report.Target)
	fmt.Fprintf(&out, "Entry: %s\n", report.Entry)
	fmt.Fprintf(&out, "Emit: %s\n", report.Emit)
	fmt.Fprintf(&out, "Run emit: %s\n", report.RunEmit)
	fmt.Fprintf(&out, "Target triple: %s\n", defaultString(report.TargetTriple, "<host-default>"))
	if len(report.Foreign) == 0 {
		out.WriteString("Foreign sources: <none>\n")
	} else {
		out.WriteString("Foreign sources:\n")
		for _, foreign := range report.Foreign {
			fmt.Fprintf(&out, "  - %s\n", foreign)
		}
	}
	if len(report.Scanned) != 0 {
		out.WriteString("Scanned native files:\n")
		for _, scanned := range report.Scanned {
			fmt.Fprintf(&out, "  - %s\n", scanned)
		}
	}
	if len(report.Contracts) != 0 {
		out.WriteString("ABI contracts:\n")
		for _, contract := range report.Contracts {
			fmt.Fprintf(&out, "  - %s\n", contract)
		}
	}
	if len(report.LinkFlags) != 0 {
		out.WriteString("Link flags:\n")
		for _, flag := range report.LinkFlags {
			fmt.Fprintf(&out, "  - %s\n", flag)
		}
	}
	if len(report.Issues) == 0 {
		out.WriteString("ABI lint: clean\n")
		return out.String(), nil
	}
	out.WriteString("ABI lint issues:\n")
	for _, issue := range report.Issues {
		loc := issue.File
		if issue.Line != 0 {
			loc = fmt.Sprintf("%s:%d", loc, issue.Line)
		}
		fmt.Fprintf(&out, "  - [%s] %s %s: %s\n", issue.Severity, issue.Code, loc, issue.Message)
	}
	return out.String(), nil
}

func nativeABILintHasErrors(report *nativeABILintReport) bool {
	if report == nil {
		return false
	}
	for _, issue := range report.Issues {
		if issue.Severity == nativeABILintError {
			return true
		}
	}
	return false
}
