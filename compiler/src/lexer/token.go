package lexer

import "fmt"

type TokenKind int

const (
	// Special
	TOKEN_EOF TokenKind = iota
	TOKEN_NEWLINE
	TOKEN_INDENT
	TOKEN_DEDENT

	// Literals
	TOKEN_IDENT
	TOKEN_INT_LIT    // 123
	TOKEN_FLOAT_LIT  // 1.5, 1e3, 2.0f32
	TOKEN_HEX_LIT    // 0xff
	TOKEN_STRING_LIT // "hello"

	// Keywords
	TOKEN_DEF
	TOKEN_ERROR
	TOKEN_ENUM
	TOKEN_STRUCT
	TOKEN_CONST
	TOKEN_GLOBAL
	TOKEN_EXTERN
	TOKEN_EXPORT
	TOKEN_IF
	TOKEN_MATCH
	TOKEN_ELIF
	TOKEN_ELSE
	TOKEN_WHILE
	TOKEN_RETURN
	TOKEN_ANY
	TOKEN_HEAP
	TOKEN_STACK
	TOKEN_STATIC
	TOKEN_MUTABLE
	TOKEN_REPR
	TOKEN_PACKED
	TOKEN_ALIGNED
	TOKEN_PASS
	TOKEN_PANIC
	TOKEN_NOT
	TOKEN_AND
	TOKEN_OR
	TOKEN_NULL
	TOKEN_TRUE
	TOKEN_FALSE
	TOKEN_ZEROED
	TOKEN_SIZEOF
	TOKEN_TAIL
	TOKEN_TRY
	TOKEN_CATCH
	TOKEN_RAISE
	TOKEN_REGION
	TOKEN_DESTROY
	TOKEN_NEW
	TOKEN_AS
	TOKEN_IN
	TOKEN_WITH
	TOKEN_CONTEXT

	// Punctuation & Operators
	TOKEN_COLON     // :
	TOKEN_ARROW     // ->
	TOKEN_DOT       // .
	TOKEN_RANGE     // ..
	TOKEN_RANGE_LT  // ..<
	TOKEN_RANGE_GT  // ..>
	TOKEN_COMMA     // ,
	TOKEN_LPAREN    // (
	TOKEN_RPAREN    // )
	TOKEN_LBRACKET  // [
	TOKEN_RBRACKET  // ]
	TOKEN_HASH      // #
	TOKEN_AMPERSAND // &
	TOKEN_QUESTION  // ?
	TOKEN_QASSIGN   // ?=
	TOKEN_BANG      // !
	TOKEN_AT        // @
	TOKEN_ELLIPSIS  // ...

	// Assignment
	TOKEN_LARROW    // <-
	TOKEN_PLUSEQ    // +=
	TOKEN_MINUSEQ   // -=
	TOKEN_STAREQ    // *=
	TOKEN_SLASHEQ   // /=
	TOKEN_PERCENTEQ // %=
	TOKEN_CARETEQ   // ^=
	TOKEN_PIPEEQ    // |=
	TOKEN_AMPEQ     // &=
	TOKEN_LSHIFTEQ  // <<=
	TOKEN_RSHIFTEQ  // >>=
	TOKEN_ASSIGN    // =

	// Comparison
	TOKEN_EQEQ   // ==
	TOKEN_BANGEQ // !=
	TOKEN_LT     // <
	TOKEN_GT     // >
	TOKEN_LTEQ   // <=
	TOKEN_GTEQ   // >=

	// Arithmetic
	TOKEN_PLUS    // +
	TOKEN_MINUS   // -
	TOKEN_STAR    // *
	TOKEN_SLASH   // /
	TOKEN_PERCENT // %

	// Bitwise
	TOKEN_PIPE   // |
	TOKEN_CARET  // ^
	TOKEN_LSHIFT // <<
	TOKEN_RSHIFT // >>
	TOKEN_TILDE  // ~

	// Surface-only and compatibility tokens appended here to preserve legacy token numbering.
	TOKEN_CHAR_LIT // 'a'
	TOKEN_IS       // is
	TOKEN_FATARROW // =>
	TOKEN_TO       // to
	TOKEN_LBRACE   // {
	TOKEN_RBRACE   // }
)

var tokenNames = map[TokenKind]string{
	TOKEN_EOF:     "EOF",
	TOKEN_NEWLINE: "NEWLINE",
	TOKEN_INDENT:  "INDENT",
	TOKEN_DEDENT:  "DEDENT",

	TOKEN_IDENT:      "IDENT",
	TOKEN_INT_LIT:    "INT",
	TOKEN_FLOAT_LIT:  "FLOAT",
	TOKEN_HEX_LIT:    "HEX",
	TOKEN_STRING_LIT: "STRING",
	TOKEN_CHAR_LIT:   "CHAR",

	TOKEN_DEF:     "def",
	TOKEN_ERROR:   "error",
	TOKEN_ENUM:    "enum",
	TOKEN_STRUCT:  "struct",
	TOKEN_CONST:   "const",
	TOKEN_GLOBAL:  "global",
	TOKEN_EXTERN:  "extern",
	TOKEN_EXPORT:  "export",
	TOKEN_IF:      "if",
	TOKEN_MATCH:   "match",
	TOKEN_ELIF:    "elif",
	TOKEN_ELSE:    "else",
	TOKEN_WHILE:   "while",
	TOKEN_RETURN:  "return",
	TOKEN_ANY:     "any",
	TOKEN_HEAP:    "heap",
	TOKEN_STACK:   "stack",
	TOKEN_STATIC:  "static",
	TOKEN_MUTABLE: "mutable",
	TOKEN_REPR:    "repr",
	TOKEN_PACKED:  "packed",
	TOKEN_ALIGNED: "aligned",
	TOKEN_PASS:    "pass",
	TOKEN_PANIC:   "panic",
	TOKEN_NOT:     "not",
	TOKEN_AND:     "and",
	TOKEN_OR:      "or",
	TOKEN_NULL:    "null",
	TOKEN_TRUE:    "true",
	TOKEN_FALSE:   "false",
	TOKEN_ZEROED:  "zeroed",
	TOKEN_SIZEOF:  "sizeof",
	TOKEN_TAIL:    "tail",
	TOKEN_TRY:     "try",
	TOKEN_CATCH:   "catch",
	TOKEN_RAISE:   "raise",
	TOKEN_REGION:  "region",
	TOKEN_DESTROY: "destroy",
	TOKEN_NEW:     "new",
	TOKEN_IS:      "is",
	TOKEN_AS:      "as",
	TOKEN_IN:      "in",
	TOKEN_WITH:    "with",
	TOKEN_CONTEXT: "context",
	TOKEN_TO:      "to",

	TOKEN_COLON:     ":",
	TOKEN_ARROW:     "->",
	TOKEN_FATARROW:  "=>",
	TOKEN_DOT:       ".",
	TOKEN_RANGE:     "..",
	TOKEN_RANGE_LT:  "..<",
	TOKEN_RANGE_GT:  "..>",
	TOKEN_COMMA:     ",",
	TOKEN_LPAREN:    "(",
	TOKEN_RPAREN:    ")",
	TOKEN_LBRACKET:  "[",
	TOKEN_RBRACKET:  "]",
	TOKEN_LBRACE:    "{",
	TOKEN_RBRACE:    "}",
	TOKEN_HASH:      "#",
	TOKEN_AMPERSAND: "&",
	TOKEN_QUESTION:  "?",
	TOKEN_QASSIGN:   "?=",
	TOKEN_BANG:      "!",
	TOKEN_AT:        "@",
	TOKEN_ELLIPSIS:  "...",

	TOKEN_LARROW:    "<-",
	TOKEN_PLUSEQ:    "+=",
	TOKEN_MINUSEQ:   "-=",
	TOKEN_STAREQ:    "*=",
	TOKEN_SLASHEQ:   "/=",
	TOKEN_PERCENTEQ: "%=",
	TOKEN_CARETEQ:   "^=",
	TOKEN_PIPEEQ:    "|=",
	TOKEN_AMPEQ:     "&=",
	TOKEN_LSHIFTEQ:  "<<=",
	TOKEN_RSHIFTEQ:  ">>=",
	TOKEN_ASSIGN:    "=",

	TOKEN_EQEQ:   "==",
	TOKEN_BANGEQ: "!=",
	TOKEN_LT:     "<",
	TOKEN_GT:     ">",
	TOKEN_LTEQ:   "<=",
	TOKEN_GTEQ:   ">=",

	TOKEN_PLUS:    "+",
	TOKEN_MINUS:   "-",
	TOKEN_STAR:    "*",
	TOKEN_SLASH:   "/",
	TOKEN_PERCENT: "%",

	TOKEN_PIPE:   "|",
	TOKEN_CARET:  "^",
	TOKEN_LSHIFT: "<<",
	TOKEN_RSHIFT: ">>",
	TOKEN_TILDE:  "~",
}

var keywords = map[string]TokenKind{
	"def":     TOKEN_DEF,
	"error":   TOKEN_ERROR,
	"enum":    TOKEN_ENUM,
	"struct":  TOKEN_STRUCT,
	"const":   TOKEN_CONST,
	"global":  TOKEN_GLOBAL,
	"extern":  TOKEN_EXTERN,
	"export":  TOKEN_EXPORT,
	"if":      TOKEN_IF,
	"match":   TOKEN_MATCH,
	"elif":    TOKEN_ELIF,
	"else":    TOKEN_ELSE,
	"while":   TOKEN_WHILE,
	"return":  TOKEN_RETURN,
	"heap":    TOKEN_HEAP,
	"stack":   TOKEN_STACK,
	"static":  TOKEN_STATIC,
	"mutable": TOKEN_MUTABLE,
	"repr":    TOKEN_REPR,
	"packed":  TOKEN_PACKED,
	"aligned": TOKEN_ALIGNED,
	"pass":    TOKEN_PASS,
	"panic":   TOKEN_PANIC,
	"not":     TOKEN_NOT,
	"and":     TOKEN_AND,
	"or":      TOKEN_OR,
	"null":    TOKEN_NULL,
	"true":    TOKEN_TRUE,
	"false":   TOKEN_FALSE,
	"zeroed":  TOKEN_ZEROED,
	"sizeof":  TOKEN_SIZEOF,
	"tail":    TOKEN_TAIL,
	"try":     TOKEN_TRY,
	"catch":   TOKEN_CATCH,
	"raise":   TOKEN_RAISE,
	"is":      TOKEN_IS,
	"as":      TOKEN_AS,
	"in":      TOKEN_IN,
	"with":    TOKEN_WITH,
	"context": TOKEN_CONTEXT,
	"to":      TOKEN_TO,
}

func LookupKeyword(ident string) TokenKind {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return TOKEN_IDENT
}

type Pos struct {
	File      string
	Line      int
	Col       int
	Offset    int
	EndLine   int
	EndCol    int
	EndOffset int
}

func (p Pos) IsZero() bool {
	return p.File == "" && p.Line == 0 && p.Col == 0 && p.Offset == 0 && p.EndLine == 0 && p.EndCol == 0 && p.EndOffset == 0
}

func (p Pos) normalizedEnd() Pos {
	if p.EndLine == 0 && p.EndCol == 0 && p.EndOffset == 0 {
		return Pos{File: p.File, Line: p.Line, Col: p.Col, Offset: p.Offset}
	}
	return Pos{File: p.File, Line: p.EndLine, Col: p.EndCol, Offset: p.EndOffset}
}

func (p Pos) WithEnd(end Pos) Pos {
	if p.IsZero() {
		p = end
	}
	if p.File == "" {
		p.File = end.File
	}
	if p.Line == 0 {
		p.Line = end.Line
	}
	if p.Col == 0 {
		p.Col = end.Col
	}
	if p.Offset == 0 && (p.Line != 0 || p.Col != 0 || end.Offset != 0) {
		p.Offset = end.Offset
	}
	p.EndLine = end.Line
	p.EndCol = end.Col
	p.EndOffset = end.Offset
	return p
}

func (p Pos) Merge(other Pos) Pos {
	if p.IsZero() {
		return other
	}
	if other.IsZero() {
		return p
	}
	return p.WithEnd(other.normalizedEnd())
}

func (p Pos) HasRange() bool {
	if p.IsZero() {
		return false
	}
	end := p.normalizedEnd()
	return end.Line != p.Line || end.Col != p.Col || end.Offset != p.Offset
}

func (p Pos) String() string {
	file := p.File
	if file == "" {
		file = "<unknown>"
	}
	if !p.HasRange() {
		return fmt.Sprintf("%s:%d:%d", file, p.Line, p.Col)
	}
	end := p.normalizedEnd()
	if end.Line == p.Line {
		return fmt.Sprintf("%s:%d:%d-%d", file, p.Line, p.Col, end.Col)
	}
	return fmt.Sprintf("%s:%d:%d-%d:%d", file, p.Line, p.Col, end.Line, end.Col)
}

type Token struct {
	Kind   TokenKind
	Text   string
	Pos    Pos
	Suffix string // for typed literals like 1234u64 -> Suffix="u64"
}

func (t Token) String() string {
	name, ok := tokenNames[t.Kind]
	if !ok {
		name = fmt.Sprintf("TokenKind(%d)", t.Kind)
	}
	if t.Text != "" && t.Text != name {
		return fmt.Sprintf("%s(%q)", name, t.Text)
	}
	return name
}

func TokenName(kind TokenKind) string {
	if name, ok := tokenNames[kind]; ok {
		return name
	}
	return fmt.Sprintf("TokenKind(%d)", kind)
}
