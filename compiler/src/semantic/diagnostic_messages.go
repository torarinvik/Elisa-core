package semantic

import "fmt"

// NoContainerRegionMessage renders the "this container has nowhere to live" family for
// container LITERALS (darray/dict/set literals, comprehensions, query expressions). A
// container's region is INFERRED from its destination, so the fix is always to give it one:
// a typed local, a matching return type, or a typed field. A bare call argument is the one
// position that supplies nothing, which is where this most often bites.
//
// This replaces the older "requires an active in <arena>: scope" wording. That text named a
// construct that is now largely deprecated, and told the reader nothing about the fix.
func NoContainerRegionMessage(subject string) string {
	return fmt.Sprintf("%s has no region to allocate in; it takes one from its destination: annotate the binding, use a matching return type, or store it in a struct field (a call argument supplies none)", subject)
}

// NoGrowthRegionMessage is the same family for the GROWING operations (push, extend, reserve,
// resize, dict/set inserts). The container already exists here, so the region comes from
// wherever the RECEIVER was declared rather than from this call site.
// NoCstrCopyRegionMessage is the container-region family for `darray.cstr`, which allocates a
// NUL-terminated COPY of the darray. Naming the copy is the informative half of the message.
func NoCstrCopyRegionMessage() string {
	return "darray cstr has no region to allocate the NUL-terminated copy in; give it a typed destination: a local, a matching return type, or a struct field"
}

func NoGrowthRegionMessage(subject string) string {
	return fmt.Sprintf("%s has no region to grow into; a region is inferred from where the receiver is declared (a typed local, a parameter, or a struct field)", subject)
}

func DuplicateDeclarationMessage(name string, kind SymbolKind) string {
	return fmt.Sprintf("duplicate declaration %q (already defined as %s)", name, kind)
}

func DuplicateExportNameMessage(name string) string {
	return fmt.Sprintf("duplicate export name %q", name)
}

// UntypedShadowMessage is the shared wording for a nested `name = value` that shadows an outer local.
func UntypedShadowMessage(name string, kind string) string {
	return fmt.Sprintf("%q = ... declares a new local that shadows the outer %s %q; write `%s <- ...` to update it, or give the new local a type to shadow it on purpose", name, kind, name, name)
}

// LostArrayParamWritesMessage is the shared wording for a by-value array parameter whose writes
// never reach anything: the parameter is a copy, so they vanish at return.
func LostArrayParamWritesMessage(name string) string {
	return fmt.Sprintf("parameter %q is a by-value array copy that is written but never read back, so the writes are lost when the function returns; take `%s: mutable T[N]&` and pass `&array` to update the caller's array", name, name)
}

func DuplicateLocalMessage(name string, kind SymbolKind) string {
	return fmt.Sprintf("duplicate local %q (already defined as %s)", name, kind)
}

func DuplicateTypeMessage(name string) string {
	return fmt.Sprintf("duplicate type %q", name)
}

func UnknownInterfaceMessage(name string) string {
	return fmt.Sprintf("unknown interface %q", name)
}

func UnknownPermissionMessage(name string) string {
	return fmt.Sprintf("unknown permission %q", name)
}

func UndefinedExportTargetMessage(name string) string {
	return fmt.Sprintf("export target %q is undefined", name)
}

func UnknownTypeMessage(name string) string {
	return fmt.Sprintf("unknown type %q", name)
}

func UndefinedIdentifierMessage(name string) string {
	return fmt.Sprintf("undefined identifier %q", name)
}

func PrivateNameMessage(qualifiedName string, owner string) string {
	return fmt.Sprintf("%q is private to module %q", visibleCallableName(qualifiedName), owner)
}
