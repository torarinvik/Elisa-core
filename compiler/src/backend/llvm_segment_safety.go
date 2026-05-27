//go:build cgo

package backend

import (
	"fmt"
	"regexp"
	"strings"
)

var llvmAttributeGroupRe = regexp.MustCompile(`attributes #([0-9]+) = \{([^}]*)\}`)
var llvmSegmentAgnosticFunctionRe = regexp.MustCompile(`(?s)define[^@]*@([A-Za-z_.$][A-Za-z0-9_.$]*)\([^)]*\)\s+#([0-9]+)[^{]*\{(.*?)\n\}`)

func validateSegmentAgnosticLLVMIR(ir string) error {
	if strings.TrimSpace(ir) == "" {
		return nil
	}
	attrsByID := map[string]string{}
	for _, match := range llvmAttributeGroupRe.FindAllStringSubmatch(ir, -1) {
		if len(match) == 3 {
			attrsByID[match[1]] = match[2]
		}
	}
	for _, match := range llvmSegmentAgnosticFunctionRe.FindAllStringSubmatch(ir, -1) {
		if len(match) != 4 {
			continue
		}
		name := match[1]
		attrs := attrsByID[match[2]]
		isSegmentAgnostic := strings.Contains(attrs, `"elisacore.segment_agnostic"="true"`)
		isAsyncEntry := strings.Contains(attrs, `"elisacore.async_entry"="true"`)
		isSegmentEstablishing := strings.Contains(attrs, `"elisacore.segment_establishing"="true"`)
		if isSegmentAgnostic && llvmAttrsContainStackProtector(attrs) {
			return fmt.Errorf("@segment_agnostic function %q lowered with stack-protector attributes {%s}; this would create a hidden Segment.Host dependency", name, attrs)
		}
		if (isAsyncEntry || isSegmentEstablishing) && llvmAttrsContainStackProtector(attrs) {
			return fmt.Errorf("segment entry function %q lowered with stack-protector attributes {%s}; @async_entry/@segment_establishing prologues must be canary-free before establishing %%fs", name, attrs)
		}
		if isSegmentAgnostic && llvmBodyContainsSegmentAccess(match[3]) {
			return fmt.Errorf("@segment_agnostic function %q lowered with a literal %%fs/%%gs access; segment-agnostic functions must not depend on host segment state", name)
		}
	}
	return nil
}

func llvmAttrsContainStackProtector(attrs string) bool {
	for _, field := range strings.Fields(attrs) {
		switch strings.Trim(field, `,`) {
		case "ssp", "sspstrong", "sspreq":
			return true
		}
	}
	return false
}

func llvmBodyContainsSegmentAccess(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "%fs") ||
		strings.Contains(lower, "%gs") ||
		strings.Contains(lower, "fs:") ||
		strings.Contains(lower, "gs:")
}
