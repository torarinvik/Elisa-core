package frontendir

import (
	"fmt"
	"sync"

	"elisacore/src/ast"
)

// BundleVersion is the version stamped into a bundle's header. It tracks the wire
// format, which v2 made explicit and self-describing; see schema.go.
const BundleVersion = SchemaVersion

type Bundle struct {
	Version        int
	SourceFilename string
	ResolvedSource []byte
	File           *ast.File
}

var (
	loadSchemaOnce sync.Once
	loadedSchema   *Schema
	loadedSchemaIn error
)

// sharedSchema derives and stamps the schema once per process. Deriving it is
// reflection over ~280 types, which is cheap but not free, and it cannot change
// during a run.
func sharedSchema() (*Schema, error) {
	loadSchemaOnce.Do(func() {
		loadedSchema, loadedSchemaIn = LoadSchema()
	})
	return loadedSchema, loadedSchemaIn
}

func Encode(bundle *Bundle) ([]byte, error) {
	if bundle == nil {
		return nil, fmt.Errorf("frontend IR bundle is nil")
	}
	if bundle.File == nil {
		return nil, fmt.Errorf("frontend IR bundle is missing AST")
	}
	schema, err := sharedSchema()
	if err != nil {
		return nil, err
	}
	copyBundle := *bundle
	if copyBundle.Version == 0 {
		copyBundle.Version = BundleVersion
	}
	return encodeBundle(schema, &copyBundle)
}

func Decode(data []byte) (*Bundle, error) {
	schema, err := sharedSchema()
	if err != nil {
		return nil, err
	}
	bundle, err := decodeBundle(schema, data)
	if err != nil {
		return nil, err
	}
	if bundle.File == nil {
		return nil, fmt.Errorf("frontend IR bundle is missing AST")
	}
	return bundle, nil
}
