#include "frontend_lexer.h"
#include <assert.h>
#include <stdint.h>

int main(void) {
    assert(frontend_lexer_parity_suite() == 1);
    assert(frontend_lexer_token_count((uint8_t *)"hello\n") == 3);
    assert(frontend_lexer_token_count_with_len((uint8_t *)"hello\n", 6) == 3);
    assert(frontend_lexer_token_count((uint8_t *)"x <- y -> z\n") == 7);
    assert(frontend_lexer_token_count((uint8_t *)"with context\n") == 4);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 0) == 53);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 1) == 5);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 2) == 2);
    assert(frontend_lexer_token_kind_at((uint8_t *)"with context\n", 3) == 1);
    assert(frontend_lexer_token_kind_at((uint8_t *)"region new destroy\n", 0) == 5);
    assert(frontend_lexer_token_kind_at((uint8_t *)"region new destroy\n", 1) == 5);
    assert(frontend_lexer_token_kind_at((uint8_t *)"region new destroy\n", 2) == 5);
    assert(frontend_lexer_token_checksum((uint8_t *)"hello\n") != 0);
    assert(frontend_lexer_token_checksum((uint8_t *)"hello\n") != frontend_lexer_token_checksum((uint8_t *)"x <- y -> z\n"));
    return 0;
}
