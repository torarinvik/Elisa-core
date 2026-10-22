package semantic

import (
	"fmt"
	"os"
	"reflect"
)

// Tracked value types are stored as private copies (every write into
// currentSpecializedValueTypes clones), and every edit builds a new type, so a
// read can hand out the stored type itself. Copying on each read was 7.5% of all
// bytes allocated by a stage1 seed. ELISA_TRACKED_SHARE_CHECK=1 keeps a deep copy
// of each shared type and panics if one changes before its function finishes.
var trackedShareCheck = os.Getenv("ELISA_TRACKED_SHARE_CHECK") != ""

type trackedShareRecord struct {
	shared   Type
	snapshot Type
}

func (a *Analyzer) shareTrackedValueType(t Type) Type {
	if trackedShareCheck && t != nil {
		var memo trackedCloneMemo
		a.trackedShareChecks = append(a.trackedShareChecks, trackedShareRecord{shared: t, snapshot: a.cloneTrackedValueTypeWithMemoDepth(t, &memo, 0)})
	}
	return t
}

func (a *Analyzer) verifyTrackedShares(where string) {
	for _, rec := range a.trackedShareChecks {
		if !reflect.DeepEqual(rec.shared, rec.snapshot) {
			panic(fmt.Sprintf("shared tracked value type %s changed in place (%s)", rec.shared, where))
		}
	}
	a.trackedShareChecks = a.trackedShareChecks[:0]
}
