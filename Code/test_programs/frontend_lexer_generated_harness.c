#include "frontend_lexer.h"
#include <assert.h>
#include <stdint.h>

int main(void) {
    assert(frontend_lexer_parity_suite() == 1);
    assert(frontend_lexer_token_count((uint8_t *)"hello\n") == 3);
    assert(frontend_lexer_token_count((uint8_t *)"x <- y -> z\n") == 7);
    return 0;
}
