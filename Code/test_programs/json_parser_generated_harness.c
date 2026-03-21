#include "json_parser.h"

#include <assert.h>
#include <stdint.h>

int main(void) {
    const uint8_t *dom_doc = (uint8_t*)"{\"na\\u006de\":\"line\\nbreak\",\"quote\":\"\\\"\",\"nums\":[123,-2],\"ok\":true,\"none\":null,\"big\":18446744073709551615}";

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

    assert(json_parser_ast_node_count((uint8_t*)"{\"items\":[1,2,3],\"ok\":true}") > 0);
    assert(json_parser_ast_object_len((uint8_t*)"{\"a\":1,\"b\":2}") == 2);
    assert(json_parser_ast_object_len((uint8_t*)"[") < 0);

    assert(json_parser_ast_field_string_eq((uint8_t*)dom_doc, (uint8_t*)"name", (uint8_t*)"line\nbreak") == 1);
    assert(json_parser_ast_field_string_eq((uint8_t*)dom_doc, (uint8_t*)"quote", (uint8_t*)"\"") == 1);
    assert(json_parser_ast_field_string_eq((uint8_t*)dom_doc, (uint8_t*)"missing", (uint8_t*)"x") < 0);

    assert(json_parser_ast_field_bool((uint8_t*)dom_doc, (uint8_t*)"ok") == 1);
    assert(json_parser_ast_field_is_null((uint8_t*)dom_doc, (uint8_t*)"none") == 1);
    assert(json_parser_ast_array_field_len((uint8_t*)dom_doc, (uint8_t*)"nums") == 2);

    int64_t first_num = 0;
    int64_t second_num = 0;
    int64_t missing_num = 99;
    uint64_t big_num = 0;
    assert(json_parser_ast_array_field_i64_at((uint8_t*)dom_doc, (uint8_t*)"nums", 0u, &first_num) == 1);
    assert(json_parser_ast_array_field_i64_at((uint8_t*)dom_doc, (uint8_t*)"nums", 1u, &second_num) == 1);
    assert(json_parser_ast_array_field_i64_at((uint8_t*)dom_doc, (uint8_t*)"nums", 2u, &missing_num) == 0);
    assert(json_parser_ast_field_u64((uint8_t*)dom_doc, (uint8_t*)"big", &big_num) == 1);
    assert(json_parser_ast_field_i64((uint8_t*)dom_doc, (uint8_t*)"missing", &first_num) == 0);
    assert(first_num == 123);
    assert(second_num == -2);
    assert(missing_num == 99);
    assert(big_num == UINT64_C(18446744073709551615));

    assert(json_parser_parallel_max_workers() >= 1);
    assert(json_parser_parallel_checksum((uint8_t*)"[1,2,3]", 1u, 3u) == 345);
    assert(json_parser_parallel_checksum((uint8_t*)"[1,2,3]", 4u, 5u) == 575);
    assert(json_parser_parallel_ast_checksum((uint8_t*)"{\"a\":1}", 2u, 4u) == 356);
    assert(json_parser_parallel_ast_cached_checksum((uint8_t*)"{\"items\":[1,2,3],\"ok\":true}", 3u, 2u) == 468);
    assert(json_parser_parallel_checksum((uint8_t*)"{\"a\":[1,2}", 2u, 4u) < 0);

    return 0;
}
