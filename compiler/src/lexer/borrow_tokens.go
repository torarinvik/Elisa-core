package lexer

// BorrowReadOnlyTokens lends the revision-owned buffer to trusted frontend code.
// Callers must not mutate the returned slice or its backing array. Parsers must
// use their borrowed-token constructors, which detach before recovery writes.
// The slice and its string views keep their backing storage alive across eviction.
func (s *SourceTokens) BorrowReadOnlyTokens() []Token { return s.tokens }
