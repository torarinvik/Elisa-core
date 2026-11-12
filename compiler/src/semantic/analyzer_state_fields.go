package semantic

import "reflect"

// analyzerStorageFields preserves field-level ownership rules for the state
// groups embedded in Analyzer as well as for its directly declared fields.
var analyzerStorageFields = func() []reflect.StructField {
	t := reflect.TypeFor[Analyzer]()
	var fields []reflect.StructField
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		grouped := false
		if field.Anonymous {
			switch field.Type {
			case reflect.TypeFor[analyzerReturnBorrowState](), reflect.TypeFor[analyzerFunctionRegionState](),
				reflect.TypeFor[analyzerControlFlowState](), reflect.TypeFor[analyzerProofSolverState](),
				reflect.TypeFor[analyzerTypeMemoState]():
				grouped = true
			}
		}
		if grouped {
			for j := 0; j < field.Type.NumField(); j++ {
				nested := field.Type.Field(j)
				nested.Index = []int{i, j}
				fields = append(fields, nested)
			}
		} else {
			fields = append(fields, field)
		}
	}
	return fields
}()
