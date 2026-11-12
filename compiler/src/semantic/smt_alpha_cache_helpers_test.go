package semantic

import "elisacore/src/smtalpha"

func smtAlphaQuery(query string) (string, bool)   { return smtalpha.Query(query) }
func smtAlphaHasInvalidControl(query string) bool { return smtalpha.HasInvalidControl(query) }
func smtAlphaValidSymbol(token string) bool       { return smtalpha.ValidSymbol(token) }
func smtAlphaSymbol(token string) string          { return smtalpha.Symbol(token) }
