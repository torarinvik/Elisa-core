package unparse

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strconv"
	"strings"
)

func (f *formatter) writeDecl(level int, decl ast.Decl) {
	if decl == nil {
		return
	}
	switch n := decl.(type) {
	case *ast.EffectDecl:
		f.writeLine(level, "effect "+n.Name+":")
		if len(n.Members) == 0 {
			f.writeLine(level+1, "pass")
			return
		}
		for _, member := range n.Members {
			f.writeLine(level+1, member)
		}
	case *ast.PermissionDecl:
		f.writeLine(level, "permission "+n.Name+":")
		if len(n.Members) == 0 {
			f.writeLine(level+1, "pass")
			return
		}
		for _, member := range n.Members {
			f.writeLine(level+1, member)
		}
	case *ast.ContextDecl:
		f.writeLine(level, "bundle "+n.Name+" implicit:")
		for _, field := range n.Fields {
			f.writeLine(level+1, formatParamDecl(field))
		}
	case *ast.ParamsDecl:
		f.writeLine(level, "bundle "+n.Name+" explicit:")
		for _, param := range n.Params {
			f.writeLine(level+1, formatParamDecl(param))
		}
	case *ast.NamespaceDecl:
		keyword := "namespace"
		if n.Module {
			keyword = "module"
		}
		if n.Const && n.Module {
			keyword = "const module"
		}
		f.writeLine(level, keyword+" "+n.Name+":")
		for i, nested := range n.Decls {
			if i > 0 && !(n.Const && n.Module) {
				f.blankLine()
			}
			if n.Const && n.Module {
				if constant, ok := nested.(*ast.ConstDecl); ok {
					f.writeConstModuleMember(level+1, constant)
					continue
				}
			}
			f.writeDecl(level+1, nested)
		}
	case *ast.UsingDecl:
		f.writeLine(level, "using "+n.Name)
	case *ast.ConstDecl:
		line := "const " + n.Name
		if n.Type != nil {
			line += ": " + formatTypeExpr(n.Type)
		}
		line += " = " + formatExpr(n.Value)
		f.writePrefixedMultiline(level, "", line)
	case *ast.TokenSetDecl:
		line := "tokenset " + n.Name
		if n.ElemType != nil {
			line += ": " + formatTypeExpr(n.ElemType)
		}
		line += " = " + formatTokenSetValue(n)
		f.writePrefixedMultiline(level, "", line)
	case *ast.CharsetDecl:
		parts := make([]string, 0, len(n.Terms))
		for _, term := range n.Terms {
			parts = append(parts, formatLexerCharClassTerm(term))
		}
		f.writeLine(level, "charset "+n.Name+" = "+strings.Join(parts, " | "))
	case *ast.KeywordMapDecl:
		line := "keywordmap " + n.Name + ": " + formatTypeExpr(n.InputType) + " -> " + formatTypeExpr(n.ReturnType) + ":"
		f.writeLine(level, line)
		for _, entry := range n.Entries {
			f.writeLine(level+1, strconv.Quote(entry.Text)+" => "+formatExpr(entry.Value))
		}
		f.writeLine(level+1, "_ => "+formatExpr(n.Fallback))
	case *ast.ConstEnumDecl:
		f.writeLine(level, "const enum "+n.Name+" of "+formatTypeExpr(n.Storage)+":")
		for _, member := range n.Members {
			line := member.Name
			if member.Value != nil {
				line += " = " + formatExpr(member.Value)
			}
			f.writeLine(level+1, line)
		}
	case *ast.ErrorDecl:
		f.writeLine(level, "error "+n.Name+":")
		for _, tag := range n.Tags {
			line := tag.Name
			if len(tag.Payload) != 0 {
				parts := make([]string, 0, len(tag.Payload))
				for _, payload := range tag.Payload {
					parts = append(parts, payload.Name+": "+formatTypeExpr(payload.Type))
				}
				line += "(" + strings.Join(parts, ", ") + ")"
			}
			f.writeLine(level+1, line)
		}
	case *ast.EffectsDecl:
		line := "effectalias " + n.Name + " ="
		if n.ErrorEffects != nil {
			line += " " + formatTypeExpr(n.ErrorEffects)
		}
		line += formatPermissionRefs(n.Permissions)
		f.writeLine(level, line)
	case *ast.GrantAliasDecl:
		parts := make([]string, 0, len(n.Refs))
		for _, ref := range n.Refs {
			parts = append(parts, formatPermissionRef(ref))
		}
		f.writeLine(level, "grant "+n.Name+" = "+strings.Join(parts, ", "))
	case *ast.GlobalDecl:
		line := "global "
		if n.Mutable {
			line += "mutable "
		}
		line += n.Name + ": " + formatTypeExpr(n.Type)
		if n.Value != nil {
			line += " = " + formatExpr(n.Value)
		}
		f.writePrefixedMultiline(level, "", line)
	case *ast.EnumDecl:
		f.writeAnnotations(level, n.Annotations)
		header := ""
		if n.Packed {
			header += "packed "
		}
		header += "enum " + n.Name + ":"
		f.writeLine(level, header)
		if len(n.Common) > 0 {
			f.writeLine(level+1, "common:")
			for _, field := range n.Common {
				f.writeField(level+2, field)
			}
		}
		for _, variant := range n.Variants {
			f.writeLine(level+1, formatEnumVariantDecl(variant))
		}
	case *ast.TreeDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "tree "+n.Name+":")
		if len(n.Common) > 0 {
			f.writeLine(level+1, "common:")
			for _, field := range n.Common {
				f.writeField(level+2, field)
			}
		}
		for _, member := range n.Members {
			f.writeTreeMember(level+1, member)
		}
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
	case *ast.StructDecl:
		f.writeAnnotations(level, n.Annotations)
		header := ""
		prefix := ""
		if n.Affine {
			header += "affine "
		}
		switch n.Layout {
		case ast.StructLayoutAOS:
			prefix = "layout aos "
		case ast.StructLayoutSOA:
			prefix = "layout soa "
		}
		header += prefix + "struct " + n.Name
		if n.RegionOwner != "" {
			header += formatGenericParams(n.GenericParams, n.TypeParams, nil, nil)
		} else {
			header += formatGenericParams(n.GenericParams, n.TypeParams, n.RegionParams, nil)
		}
		if n.RegionOwner != "" {
			header += " in " + n.RegionOwner
		}
		header += formatAggregateStateSuffix(n.HasStateParam, n.StateParamCount)
		switch n.Layout {
		case ast.StructLayoutC:
			header += " layout c"
		case ast.StructLayoutPacked:
			header += " layout packed"
		}
		header += ":"
		f.writeLine(level, header)
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	case *ast.StoreDecl:
		f.writeAnnotations(level, n.Annotations)
		keyword := "store"
		if n.Soa {
			keyword = "soa"
		}
		f.writeLine(level, keyword+" "+n.Name+":")
		for _, field := range n.Fields {
			f.writeField(level+1, field)
		}
	case *ast.InterfaceDecl:
		header := "static interface "
		if n.Protocol {
			header = "protocol "
		}
		f.writeLine(level, header+n.Name+":")
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.AssociatedTypeDecl:
				f.writeLine(level+1, "type "+m.Name)
			case *ast.ExternFuncDecl:
				f.writeLine(level+1, formatImplMethodHeader(m.Name, m.GenericParams, m.TypeParams, m.RegionParams, m.PermissionParams, m.Params, m.ParamPacks, m.ParamItemOrder, m.ImplicitParams, m.ImplicitBundles, m.ImplicitItemOrder, m.ReturnType, m.EffectAlias, m.Effects, m.Permissions, m.Ensures, m.Variadic))
			}
		}
	case *ast.ImplDecl:
		f.writeAnnotations(level, n.Annotations)
		header := ""
		if n.IsExtension() {
			header = "impl " + formatTypeExpr(n.ForType) + ":"
		} else {
			header = "impl " + n.InterfaceName + " for " + formatTypeExpr(n.ForType) + ":"
		}
		f.writeLine(level, header)
		for _, member := range n.Members {
			switch m := member.(type) {
			case *ast.ImplAssociatedTypeDecl:
				f.writeLine(level+1, "type "+m.Name+" = "+formatTypeExpr(m.Type))
			case *ast.FuncDecl:
				f.writeAnnotations(level+1, m.Annotations)
				header := formatFuncHeader(m.Name, m.GenericParams, m.TypeParams, m.RegionParams, m.PermissionParams, m.Params, m.ParamPacks, m.ParamItemOrder, m.ImplicitParams, m.ImplicitBundles, m.ImplicitItemOrder, m.ReturnType, m.EffectAlias, m.Effects, m.Permissions, m.Ensures, false)
				if m.Override {
					header = "override " + header
				}
				f.writeLine(level+1, header)
				for _, stmt := range m.Body {
					f.writeStmt(level+2, stmt)
				}
			case *ast.ExternFuncDecl:
				f.writeAnnotations(level+1, m.Annotations)
				header := formatImplMethodHeader(m.Name, m.GenericParams, m.TypeParams, m.RegionParams, m.PermissionParams, m.Params, m.ParamPacks, m.ParamItemOrder, m.ImplicitParams, m.ImplicitBundles, m.ImplicitItemOrder, m.ReturnType, m.EffectAlias, m.Effects, m.Permissions, m.Ensures, m.Variadic)
				if m.Override {
					header = "override " + header
				}
				f.writeLine(level+1, header)
			}
		}
	case *ast.FuncDecl:
		f.writeAnnotations(level, n.Annotations)
		header := formatFuncHeader(n.Name, n.GenericParams, n.TypeParams, n.RegionParams, n.PermissionParams, n.Params, n.ParamPacks, n.ParamItemOrder, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, n.ReturnType, n.EffectAlias, n.Effects, n.Permissions, n.Ensures, false)
		if n.Static {
			header = "static " + header
		}
		f.writeLine(level, header)
		for _, stmt := range n.Body {
			f.writeStmt(level+1, stmt)
		}
	case *ast.ExternFuncDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, formatExternFuncHeader(n.Name, n.GenericParams, n.TypeParams, n.RegionParams, n.PermissionParams, n.Params, n.ParamPacks, n.ParamItemOrder, n.ImplicitParams, n.ImplicitBundles, n.ImplicitItemOrder, n.ReturnType, n.EffectAlias, n.Effects, n.Permissions, n.Ensures, n.Variadic))
	case *ast.ExternVarDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "extern "+n.Name+": "+formatTypeExpr(n.Type))
	case *ast.ExternTypeDecl:
		f.writeAnnotations(level, n.Annotations)
		f.writeLine(level, "extern "+n.Name)
	case *ast.TypeAliasDecl:
		f.writeLine(level, "type "+n.Name+" = "+formatTypeExpr(n.Target))
	case *ast.ExportTypeDecl:
		f.writeLine(level, "export type "+formatTypeExpr(n.ExportedType)+" as "+n.Alias)
	case *ast.ExportFuncDecl:
		f.writeLine(level, formatExportFuncHeader(n))
	case *ast.ExportGlobalDecl:
		line := "export global " + n.TargetName
		if n.Alias != "" && n.Alias != n.TargetName {
			line += " as " + n.Alias
		}
		f.writeLine(level, line)
	case *ast.StaticIfDecl:
		f.writeLine(level, "static if "+formatExpr(n.Cond)+":")
		for _, nested := range n.Then {
			f.writeDecl(level+1, nested)
		}
		for _, elif := range n.Elifs {
			f.writeLine(level, "static elif "+formatExpr(elif.Cond)+":")
			for _, nested := range elif.Body {
				f.writeDecl(level+1, nested)
			}
		}
		if len(n.Else) > 0 {
			f.writeLine(level, "static else:")
			for _, nested := range n.Else {
				f.writeDecl(level+1, nested)
			}
		}
	case *ast.StaticAssertDecl:
		if n.Message != nil {
			f.writePrefixedMultiline(level, "", "static assert "+formatExpr(n.Cond)+", "+formatExpr(n.Message))
		} else {
			f.writePrefixedMultiline(level, "", "static assert "+formatExpr(n.Cond))
		}
	case *ast.StaticAssertBlockDecl:
		f.writeLine(level, "static assert:")
		for _, item := range n.Assertions {
			if item.Message != nil {
				f.writePrefixedMultiline(level+1, "", formatExpr(item.Cond)+", "+formatExpr(item.Message))
			} else {
				f.writePrefixedMultiline(level+1, "", formatExpr(item.Cond))
			}
		}
	case *ast.StaticGenerateDecl:
		f.writeLine(level, "static generate:")
		for _, stmt := range n.Body {
			f.writeStaticGenerateStmt(level+1, stmt)
		}
	}
}

func (f *formatter) writeConstModuleMember(level int, n *ast.ConstDecl) {
	line := n.Name
	if n.Type != nil {
		line += ": " + formatTypeExpr(n.Type)
	}
	line += " = " + formatExpr(n.Value)
	f.writePrefixedMultiline(level, "", line)
}

func (f *formatter) writeStaticGenerateStmt(level int, stmt ast.StaticGenerateStmt) {
	switch n := stmt.(type) {
	case *ast.StaticGenerateEmitDecl:
		f.writeLine(level, "emit "+formatStaticGenerateTokens(n.Tokens))
	case *ast.StaticGenerateForDecl:
		header := "for " + n.Name + " in " + formatExpr(n.Source)
		if n.Filter != nil {
			header += " where " + formatExpr(n.Filter)
		}
		f.writeLine(level, header+":")
		for _, inner := range n.Body {
			f.writeStaticGenerateStmt(level+1, inner)
		}
	case *ast.StaticGenerateIfDecl:
		f.writeLine(level, "if "+formatExpr(n.Cond)+":")
		for _, inner := range n.Then {
			f.writeStaticGenerateStmt(level+1, inner)
		}
		for _, elif := range n.Elifs {
			f.writeLine(level, "elif "+formatExpr(elif.Cond)+":")
			for _, inner := range elif.Body {
				f.writeStaticGenerateStmt(level+1, inner)
			}
		}
		if len(n.Else) != 0 {
			f.writeLine(level, "else:")
			for _, inner := range n.Else {
				f.writeStaticGenerateStmt(level+1, inner)
			}
		}
	}
}

func formatStaticGenerateTokens(tokens []lexer.Token) string {
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok.Kind == lexer.TOKEN_NEWLINE || tok.Kind == lexer.TOKEN_INDENT || tok.Kind == lexer.TOKEN_DEDENT {
			continue
		}
		if tok.Text != "" {
			parts = append(parts, tok.Text)
		} else {
			parts = append(parts, lexer.TokenName(tok.Kind))
		}
	}
	return strings.Join(parts, " ")
}
