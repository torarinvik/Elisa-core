#include "json_parser.h"

#include <assert.h>
#include <stdint.h>

int main(void) {
    assert(json_parser_parity_suite() == 1);

    assert(json_parser_checksum((uint8_t*)"{}") == 47);
    assert(json_parser_checksum((uint8_t*)"[]") == 43);
    assert(json_parser_checksum((uint8_t*)"{\"a\":1}") == 89);
    assert(json_parser_checksum((uint8_t*)"[true,false,null]") == 152);
    assert(json_parser_checksum((uint8_t*)"{\"items\":[1,2,3],\"ok\":true}") == 234);
    assert(json_parser_checksum((uint8_t*)"{\"a\":[1,2}") < 0);

    assert(json_parser_ast_checksum((uint8_t*)"{}") == 47);
    assert(json_parser_ast_checksum((uint8_t*)"[]") == 43);
    assert(json_parser_ast_checksum((uint8_t*)"{\"a\":1}") == 89);
    assert(json_parser_ast_checksum((uint8_t*)"[true,false,null]") == 152);
    assert(json_parser_ast_checksum((uint8_t*)"{\"items\":[1,2,3],\"ok\":true}") == 234);
    assert(json_parser_ast_checksum((uint8_t*)"{\"a\":[1,2}") < 0);

    assert(json_parser_parallel_max_workers() >= 1);
    assert(json_parser_parallel_checksum((uint8_t*)"[1,2,3]", 1u, 3u) == 345);
    assert(json_parser_parallel_checksum((uint8_t*)"[1,2,3]", 4u, 5u) == 575);
    assert(json_parser_parallel_ast_checksum((uint8_t*)"{\"a\":1}", 2u, 4u) == 356);
    assert(json_parser_parallel_ast_cached_checksum((uint8_t*)"{\"items\":[1,2,3],\"ok\":true}", 3u, 2u) == 468);
    assert(json_parser_parallel_checksum((uint8_t*)"{\"a\":[1,2}", 2u, 4u) < 0);

    return 0;
}
