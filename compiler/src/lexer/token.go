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
	TOKEN_HEX_LIT    // 0xff
	TOKEN_STRING_LIT // "hello"

	// Keywords
	TOKEN_DEF
	TOKEN_ERROR
	TOKEN_STRUCT
	TOKEN_CONST
	TOKEN_GLOBAL
	TOKEN_EXTERN
	TOKEN_EXPORT
	TOKEN_IF
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
	TOKEN_RAISE
	TOKEN_REGION
	TOKEN_DESTROY
	TOKEN_NEW
	TOKEN_AS
	TOKEN_IN

	// Punctuation & Operators
	TOKEN_COLON     // :
	TOKEN_ARROW     // ->
	TOKEN_DOT       // .
	TOKEN_COMMA     // ,
	TOKEN_LPAREN    // (
	TOKEN_RPAREN    // )
	TOKEN_LBRACKET  // [
	TOKEN_RBRACKET  // ]
	TOKEN_HASH      // #
	TOKEN_AMPERSAND // &
	TOKEN_QUESTION  // ?
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
)

var tokenNames = map[TokenKind]string{
	TOKEN_EOF:     "EOF",
	TOKEN_NEWLINE: "NEWLINE",
	TOKEN_INDENT:  "INDENT",
	TOKEN_DEDENT:  "DEDENT",

	TOKEN_IDENT:      "IDENT",
	TOKEN_INT_LIT:    "INT",
	TOKEN_HEX_LIT:    "HEX",
	TOKEN_STRING_LIT: "STRING",

	TOKEN_DEF:     "def",
	TOKEN_ERROR:   "error",
	TOKEN_STRUCT:  "struct",
	TOKEN_CONST:   "const",
	TOKEN_GLOBAL:  "global",
	TOKEN_EXTERN:  "extern",
	TOKEN_EXPORT:  "export",
	TOKEN_IF:      "if",
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
	TOKEN_RAISE:   "raise",
	TOKEN_REGION:  "region",
	TOKEN_DESTROY: "destroy",
	TOKEN_NEW:     "new",
	TOKEN_AS:      "as",
	TOKEN_IN:      "in",

	TOKEN_COLON:     ":",
	TOKEN_ARROW:     "->",
	TOKEN_DOT:       ".",
	TOKEN_COMMA:     ",",
	TOKEN_LPAREN:    "(",
	TOKEN_RPAREN:    ")",
	TOKEN_LBRACKET:  "[",
	TOKEN_RBRACKET:  "]",
	TOKEN_HASH:      "#",
	TOKEN_AMPERSAND: "&",
	TOKEN_QUESTION:  "?",
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
	"struct":  TOKEN_STRUCT,
	"const":   TOKEN_CONST,
	"global":  TOKEN_GLOBAL,
	"extern":  TOKEN_EXTERN,
	"export":  TOKEN_EXPORT,
	"if":      TOKEN_IF,
	"elif":    TOKEN_ELIF,
	"else":    TOKEN_ELSE,
	"while":   TOKEN_WHILE,
	"return":  TOKEN_RETURN,
	"any":     TOKEN_ANY,
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
	"raise":   TOKEN_RAISE,
	"as":      TOKEN_AS,
	"in":      TOKEN_IN,
}

func LookupKeyword(ident string) TokenKind {
	if tok, ok := keywords[ident]; ok {
		return tok
	}
	return TOKEN_IDENT
}

type Pos struct {
	File   string
	Line   int
	Col    int
	Offset int
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
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
