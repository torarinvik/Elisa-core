package semantic

import "elisacore/src/smtquery"

func smtAbstractXorRead(term string) (string, string, bool) { return smtquery.AbstractXorRead(term) }
