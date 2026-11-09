package unparse

import (
	"elisacore/src/ast"
	"strconv"
	"strings"
)

func (f *formatter) writeGrammarDecl(level int, decl ast.Decl) {
	switch n := decl.(type) {
	case *ast.GrammarDecl:
		header := "grammar " + n.Name
		if n.Extend {
			header = "extend grammar " + n.Name
		}
		header += formatGenericParams(n.GenericParams, n.TypeParams, n.RegionParams, n.PermissionParams)
		if n.EnvType != nil {
			header += " with " + formatTypeExpr(n.EnvType)
		}
		if n.OverType != nil {
			header += " over " + formatTypeExpr(n.OverType)
		}
		if n.UsingType != nil {
			header += " using " + formatTypeExpr(n.UsingType)
		}
		if len(n.Uses) != 0 {
			parts := make([]string, 0, len(n.Uses))
			for _, used := range n.Uses {
				parts = append(parts, formatTypeExpr(used))
			}
			header += " uses " + strings.Join(parts, ", ")
		}
		header += ":"
		f.writeLine(level, header)
		if n.ErrorType != nil {
			f.writeLine(level+1, "error "+formatTypeExpr(n.ErrorType))
		}
		if n.CursorExpr != nil {
			f.writeLine(level+1, "cursor "+formatExpr(n.CursorExpr))
		}
		if n.AllocExpr != nil {
			f.writeLine(level+1, "alloc "+formatExpr(n.AllocExpr))
		}
		if n.TokenKindType != nil {
			f.writeLine(level+1, "token_kind "+formatTypeExpr(n.TokenKindType))
		}
		if n.TokenEnumName != "" {
			line := "token_enum " + n.TokenEnumName
			if n.TokenEnumStorage != nil {
				line += " of " + formatTypeExpr(n.TokenEnumStorage)
			}
			f.writeLine(level+1, line)
		}
		if n.EOFExpr != nil {
			f.writeLine(level+1, "eof "+formatExpr(n.EOFExpr))
		}
		if n.TokenKindField != "" {
			f.writeLine(level+1, "token_field "+n.TokenKindField)
		}
		if n.CurrentFunc != "" {
			f.writeLine(level+1, "current "+n.CurrentFunc)
		}
		if n.AdvanceFunc != "" {
			f.writeLine(level+1, "advance "+n.AdvanceFunc)
		}
		if n.ExpectFunc != "" {
			f.writeLine(level+1, "expect "+n.ExpectFunc)
		}
		if n.ExpectKindFunc != "" {
			f.writeLine(level+1, "expect_kind "+n.ExpectKindFunc)
		}
		if n.RecordErrorFunc != "" {
			f.writeLine(level+1, "record_error "+n.RecordErrorFunc)
		}
		if n.TokenLookupFunc != "" {
			f.writeLine(level+1, "token_lookup "+n.TokenLookupFunc)
		}
		if n.TokenLookupCompareFunc != "" {
			f.writeLine(level+1, "token_lookup_compare "+n.TokenLookupCompareFunc)
		}
		if len(n.TokenAliases) != 0 {
			f.writeLine(level+1, "token:")
			for _, alias := range n.TokenAliases {
				line := alias.Kind
				if alias.HasLiteral {
					line += " " + strconv.Quote(alias.Literal)
				}
				f.writeLine(level+2, line)
			}
		}
		for _, channel := range n.Channels {
			f.writeGrammarChannelDecl(level+1, channel)
		}
		for _, tokenSet := range n.TokenSets {
			f.writeGrammarTokenSetDecl(level+1, tokenSet)
		}
		for _, alias := range n.GrammarAliases {
			f.writeGrammarAliasDecl(level+1, alias)
		}
		for _, grammarFn := range n.GrammarFns {
			f.writeGrammarFnDecl(level+1, grammarFn)
		}
		for _, recovery := range n.RecoveryPolicies {
			f.writeGrammarRecoveryDecl(level+1, recovery)
		}
		for _, table := range n.InfixTables {
			f.writeGrammarInfixTableDecl(level+1, table)
		}
		for index := 0; index < len(n.Productions); {
			production := n.Productions[index]
			if !production.Append {
				f.writeGrammarProduction(level+1, production)
				index++
				continue
			}
			end := index + 1
			for end < len(n.Productions) {
				next := n.Productions[end]
				if !next.Append || next.Name != production.Name || next.Public != production.Public {
					break
				}
				end++
			}
			if end-index > 1 {
				f.writeGroupedGrammarAppendProductions(level+1, n.Productions[index:end])
				index = end
				continue
			}
			f.writeGrammarProduction(level+1, production)
			index++
		}
	case *ast.GrammarEnvDecl:
		header := "grammarenv " + n.Name
		if n.OverType != nil {
			header += " over " + formatTypeExpr(n.OverType)
		}
		if n.UsingType != nil {
			header += " using " + formatTypeExpr(n.UsingType)
		}
		header += ":"
		f.writeLine(level, header)
		if n.ErrorType != nil {
			f.writeLine(level+1, "error "+formatTypeExpr(n.ErrorType))
		}
		if n.CursorExpr != nil {
			f.writeLine(level+1, "cursor "+formatExpr(n.CursorExpr))
		}
		if n.AllocExpr != nil {
			f.writeLine(level+1, "alloc "+formatExpr(n.AllocExpr))
		}
		if n.TokenKindType != nil {
			f.writeLine(level+1, "token_kind "+formatTypeExpr(n.TokenKindType))
		}
		if n.TokenGrammarName != "" {
			f.writeLine(level+1, "tokens "+n.TokenGrammarName)
		}
		if n.EOFExpr != nil {
			f.writeLine(level+1, "eof "+formatExpr(n.EOFExpr))
		}
		if n.TokenKindField != "" {
			f.writeLine(level+1, "token_field "+n.TokenKindField)
		}
		if n.CurrentFunc != "" {
			f.writeLine(level+1, "current "+n.CurrentFunc)
		}
		if n.AdvanceFunc != "" {
			f.writeLine(level+1, "advance "+n.AdvanceFunc)
		}
		if n.ExpectFunc != "" {
			f.writeLine(level+1, "expect "+n.ExpectFunc)
		}
		if n.ExpectKindFunc != "" {
			f.writeLine(level+1, "expect_kind "+n.ExpectKindFunc)
		}
		if n.RecordErrorFunc != "" {
			f.writeLine(level+1, "record_error "+n.RecordErrorFunc)
		}
	case *ast.LexerDecl:
		f.writeLine(level, "lexer "+n.Name+":")
		if n.TokenKindType != nil {
			f.writeLine(level+1, "token_kind "+formatTypeExpr(n.TokenKindType))
		}
		if n.ModeEnumName != "" {
			f.writeLine(level+1, "mode_enum "+n.ModeEnumName)
		}
		if n.GrammarName != "" {
			f.writeLine(level+1, "tokens "+n.GrammarName)
		}
		if n.KeywordCompareFunc != "" {
			f.writeLine(level+1, "keyword_compare "+n.KeywordCompareFunc)
		}
		for _, mode := range n.Modes {
			f.writeLine(level+1, "mode "+mode.Name)
		}
		for _, class := range n.CharClasses {
			parts := make([]string, 0, len(class.Terms))
			for _, term := range class.Terms {
				parts = append(parts, formatLexerCharClassTerm(term))
			}
			f.writeLine(level+1, "charclass "+class.Name+" = "+strings.Join(parts, " | "))
		}
		if n.Keywords != nil {
			line := "keywords"
			if n.Keywords.Fallback != "" {
				line += " fallback " + n.Keywords.Fallback
			}
			line += ":"
			f.writeLine(level+1, line)
			for _, entry := range n.Keywords.Entries {
				f.writeLine(level+2, strconv.Quote(entry.Text)+" -> "+entry.Kind)
			}
		}
		if n.Literals != nil {
			line := "literals"
			if n.Literals.Longest {
				line += " longest"
			}
			if n.Literals.Fallback != "" {
				line += " fallback " + n.Literals.Fallback
			}
			line += ":"
			f.writeLine(level+1, line)
			for _, entry := range n.Literals.Entries {
				f.writeLine(level+2, strconv.Quote(entry.Text)+" -> "+entry.Kind)
			}
		}
	}
}
