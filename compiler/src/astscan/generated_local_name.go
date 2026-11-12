package astscan

import "elisacore/src/generatednames"

func IsLocalGeneratedName(name string) bool    { return generatednames.IsLocalGeneratedName(name) }
func retainedLocalGensymName(name string) bool { return generatednames.IsLocalGeneratedName(name) }
