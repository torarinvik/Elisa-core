package semantic

import "fmt"

func DuplicateDeclarationMessage(name string, kind SymbolKind) string {
	return fmt.Sprintf("duplicate declaration %q (already defined as %s)", name, kind)
}

func DuplicateExportNameMessage(name string) string {
	return fmt.Sprintf("duplicate export name %q", name)
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
