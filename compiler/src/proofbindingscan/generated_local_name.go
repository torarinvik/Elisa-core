package proofbindingscan

import "elisacore/src/generatednames"

func retainedLocalGensymName(name string) bool { return generatednames.IsLocalGeneratedName(name) }
