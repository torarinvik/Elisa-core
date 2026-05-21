#include "frontend_lexer.h"
#include <assert.h>
#include <stdint.h>

int main(void) {
    assert(frontend_lexer_parity_suite() == 1);
    assert(frontend_lexer_token_count((uint8_t *)"hello\n") == 3);
    assert(frontend_lexer_token_count_with_len((uint8_t *)"hello\n", 6u) == 3);
    assert(frontend_lexer_token_count((uint8_t *)"x <- y -> z\n") == 7);
    assert(frontend_lexer_token_count((uint8_t *)"with context\n") == 4);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 0u) == 55u);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 1u) == 56u);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 2u) == 2u);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 3u) == 1u);
    assert(frontend_lexer_token_kind_at((uint8_t *)"region new destroy\n", 0u) == 5u);
    assert(frontend_lexer_token_kind_at((uint8_t *)"region new destroy\n", 1u) == 5u);
    assert(frontend_lexer_token_kind_at((uint8_t *)"region new destroy\n", 2u) == 5u);
    assert(frontend_lexer_token_checksum((uint8_t *)"hello\n") != 0);
    assert(frontend_lexer_token_checksum((uint8_t *)"hello\n") != frontend_lexer_token_checksum((uint8_t *)"x <- y -> z\n"));
    return 0;
}
