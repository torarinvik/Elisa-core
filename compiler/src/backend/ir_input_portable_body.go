package backend

import (
	"fmt"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// irPortableBodyObservation contains stable source graph locators rather than
// process-local pointers. It is diagnostic data only: declarations and facts
// are not content-encoded and an observation is not a reuse certificate.
type irPortableBodyObservation struct {
	Declaration *semantic.DiagnosticFunctionLocator
	Canonical   *semantic.DiagnosticFunctionLocator
	Use         string
	// Position-independent AST syntax observed at lowering time; this is not
	// a fingerprint of proof facts, type bindings, or source coordinates.
	SyntaxDigest [32]byte
	// A mapped private body is not identified or compared by this record.
	// Its template locator is evidence of provenance, not a body digest.
	PrivateBodyUnverified bool
}

// portableBodyObservations rejects ambiguous or unproven source identities.
// Authenticated private-to-template mappings retain an unverified body marker.
// No result of this function grants permission to reuse lowered IR.
func (r irInputReadReport) portableBodyObservations(file *ast.File) ([]irPortableBodyObservation, error) {
	index, err := semantic.DiagnosticFunctionLocators(file)
	if err != nil {
		return nil, err
	}
	out := make([]irPortableBodyObservation, 0, len(r.Bodies))
	for _, read := range r.Bodies {
		if !read.SyntaxDigestValid {
			return nil, fmt.Errorf("body observation has no authenticated syntax digest")
		}
		decl, ok := index[read.Declaration]
		if !ok {
			if !read.CanonicalFromResult || read.Canonical == nil || read.Canonical == read.Declaration {
				return nil, fmt.Errorf("body observation has no authenticated source declaration locator")
			}
			canonical, found := index[read.Canonical]
			if !found {
				return nil, fmt.Errorf("private body has no canonical source locator")
			}
			out = append(out, irPortableBodyObservation{Canonical: &canonical, Use: read.Use, SyntaxDigest: read.SyntaxDigest, PrivateBodyUnverified: true})
			continue
		}
		row := irPortableBodyObservation{Declaration: &decl, Use: read.Use, SyntaxDigest: read.SyntaxDigest}
		if read.Canonical != nil {
			canonical, ok := index[read.Canonical]
			if !ok {
				return nil, fmt.Errorf("body observation has no canonical declaration locator")
			}
			row.Canonical = &canonical
		}
		out = append(out, row)
	}
	return out, nil
}
