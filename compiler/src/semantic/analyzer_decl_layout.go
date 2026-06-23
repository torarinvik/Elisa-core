package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/easm"
)

// docs/107 increment 5 — in-source `layout` declaration registration.
//
// A `.elisa` program declares a guest-memory carrier layout (the description a `GuestVAddr[Name]`
// accessor resolves against) with a top-level `layout` declaration. collectOverlayLayouts runs in the
// early collect pre-pass (alongside collectNamedTypes etc., BEFORE any function body is analyzed) so
// the carrier types and the accessors in bodies can both see the registered layout. Each LayoutDecl
// is lowered to the SAME easm.Layout model the EASM-side `N(%base)` checks use (docs/104), keeping a
// single source of truth: the boot-assembly reads and these high-level reads cannot drift.
//
// This composes with AnalyzeOptions.OverlayLayouts (the test/embedding path): both feed the one
// overlayLayouts registry. A source layout and an option layout of the same name collide and the
// source declaration wins (it is the program's own description); a duplicate source name is an error.

// overlayFieldWidth maps a layout field's scalar type name to its byte width. Only the fixed-width
// unsigned scalars have an accessor (ReadU<N>/WriteU<N>); anything else yields width 0, which the
// accessor desugar later rejects as overlay-field-width-mismatch.
func overlayFieldWidth(typeName string) int {
	switch typeName {
	case "u8":
		return 1
	case "u16":
		return 2
	case "u32":
		return 4
	case "u64":
		return 8
	}
	return 0
}

// collectOverlayLayouts registers every in-source `layout` declaration into the overlay registry,
// lowering it to an easm.Layout. Field offsets/sizes/requires-tags are carried verbatim; the field
// width is derived from its scalar type. Reports duplicate layout names and unknown field types.
func (a *Analyzer) collectOverlayLayouts(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.LayoutDecl)
		if !ok {
			continue
		}
		if a.overlayLayouts == nil {
			a.overlayLayouts = map[string]*easm.Layout{}
		}
		if _, exists := a.overlaySourceLayouts[decl.Name]; exists {
			a.errorf(decl.Pos(), "duplicate layout %q", decl.Name)
			continue
		}
		layout := &easm.Layout{Name: decl.Name, Size: decl.Size}
		seen := map[string]bool{}
		for _, f := range decl.Fields {
			width := overlayFieldWidth(f.Type)
			if width == 0 {
				a.errorf(f.Position, "layout %s field %q has type %q, which has no fixed-width accessor (expected u8/u16/u32/u64)", decl.Name, f.Name, f.Type)
			}
			if seen[f.Name] {
				a.errorf(f.Position, "duplicate field %q in layout %s", f.Name, decl.Name)
				continue
			}
			seen[f.Name] = true
			layout.Fields = append(layout.Fields, easm.LayoutField{
				Offset:              f.Offset,
				Name:                f.Name,
				Type:                f.Type,
				Width:               width,
				RequiresSizeAtLeast: f.RequiresSizeAtLeast,
			})
		}
		a.overlayLayouts[decl.Name] = layout
		if a.overlaySourceLayouts == nil {
			a.overlaySourceLayouts = map[string]bool{}
		}
		a.overlaySourceLayouts[decl.Name] = true
	}
}
