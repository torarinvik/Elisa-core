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
    assert(json_parser_ast_field_kind((uint8_t*)dom_doc, (uint8_t*)"name") == 3);
    assert(json_parser_ast_field_kind((uint8_t*)dom_doc, (uint8_t*)"nums") == 4);
    assert(json_parser_ast_field_kind((uint8_t*)dom_doc, (uint8_t*)"missing") < 0);
    assert(json_parser_ast_array_field_len((uint8_t*)dom_doc, (uint8_t*)"nums") == 2);
    assert(json_parser_ast_object_field_len((uint8_t*)"{\"meta\":{\"ok\":true,\"none\":null},\"nums\":[1,2]}", (uint8_t*)"meta") == 2);
    assert(json_parser_ast_object_field_len((uint8_t*)"{\"meta\":{\"ok\":true,\"none\":null},\"nums\":[1,2]}", (uint8_t*)"nums") < 0);

    int64_t first_num = 0;
    int64_t second_num = 0;
    int64_t object_second = 0;
    int64_t array_root_second = 0;
    int64_t object_array_kind = -1;
    int64_t object_object_kind = -1;
    int64_t array_string_kind = -1;
    int64_t array_array_kind = -1;
    int64_t array_object_kind = -1;
    int64_t object_bool = -1;
    int64_t object_null = -1;
    int64_t array_bool = -1;
    int64_t array_null = -1;
    int64_t missing_num = 99;
    uint64_t big_num = 0;
    assert(json_parser_ast_array_field_i64_at((uint8_t*)dom_doc, (uint8_t*)"nums", 0u, &first_num) == 1);
    assert(json_parser_ast_array_field_i64_at((uint8_t*)dom_doc, (uint8_t*)"nums", 1u, &second_num) == 1);
    assert(json_parser_ast_array_field_i64_at((uint8_t*)dom_doc, (uint8_t*)"nums", 2u, &missing_num) == 0);
    assert(json_parser_ast_array_field_kind_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 0u) == 3);
    assert(json_parser_ast_array_field_string_eq_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 0u, (uint8_t*)"Ada") == 1);
    assert(json_parser_ast_array_field_bool_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 1u) == 1);
    assert(json_parser_ast_array_field_is_null_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 2u) == 1);
    assert(json_parser_ast_array_field_array_len_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 3u) == 2);
    assert(json_parser_ast_array_field_object_len_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 4u) == 1);
    assert(json_parser_ast_array_field_string_eq_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 1u, (uint8_t*)"Ada") == 0);
    assert(json_parser_ast_array_field_bool_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 0u) < 0);
    assert(json_parser_ast_array_field_kind_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 5u) < 0);
    assert(json_parser_ast_array_field_object_len_at((uint8_t*)"{\"items\":[\"Ada\",true,null,[1,2],{\"ok\":true}]}", (uint8_t*)"items", 3u) < 0);
    assert(json_parser_ast_object_key_eq_at((uint8_t*)"{\"first\":1,\"second\":2,\"third\":3}", 0u, (uint8_t*)"first") == 1);
    assert(json_parser_ast_object_key_eq_at((uint8_t*)"{\"first\":1,\"second\":2,\"third\":3}", 1u, (uint8_t*)"second") == 1);
    assert(json_parser_ast_object_key_eq_at((uint8_t*)"{\"first\":1,\"second\":2,\"third\":3}", 3u, (uint8_t*)"missing") < 0);
    assert(json_parser_ast_object_field_i64_at((uint8_t*)"{\"first\":1,\"second\":2,\"third\":3}", 1u, &object_second) == 1);
    assert(json_parser_ast_object_value_string_eq_at((uint8_t*)"{\"name\":\"Ada\",\"ok\":true,\"none\":null}", 0u, (uint8_t*)"Ada") == 1);
    assert(json_parser_ast_object_field_bool_at((uint8_t*)"{\"name\":\"Ada\",\"ok\":true,\"none\":null}", 1u) == 1);
    assert(json_parser_ast_object_field_is_null_at((uint8_t*)"{\"name\":\"Ada\",\"ok\":true,\"none\":null}", 2u) == 1);
    assert(json_parser_ast_object_value_kind_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 1u) == 4);
    assert(json_parser_ast_object_value_kind_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 2u) == 5);
    assert(json_parser_ast_object_value_array_len_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 1u) == 2);
    assert(json_parser_ast_object_value_object_len_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 2u) == 1);
    assert(json_parser_ast_object_value_array_len_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 0u) < 0);
    assert(json_parser_ast_array_i64_at((uint8_t*)"[4,5,6]", 1u, &array_root_second) == 1);
    assert(json_parser_ast_array_kind_at((uint8_t*)"[1,\"Ada\",false,null,[2],{\"ok\":true}]", 1u) == 3);
    assert(json_parser_ast_array_kind_at((uint8_t*)"[1,\"Ada\",false,null,[2],{\"ok\":true}]", 4u) == 4);
    assert(json_parser_ast_array_kind_at((uint8_t*)"[1,\"Ada\",false,null,[2],{\"ok\":true}]", 5u) == 5);
    assert(json_parser_ast_array_array_len_at((uint8_t*)"[1,\"Ada\",false,null,[2,3],{\"ok\":true,\"n\":1}]", 4u) == 2);
    assert(json_parser_ast_array_object_len_at((uint8_t*)"[1,\"Ada\",false,null,[2,3],{\"ok\":true,\"n\":1}]", 5u) == 2);
    assert(json_parser_ast_array_object_len_at((uint8_t*)"[1,\"Ada\",false,null,[2,3],{\"ok\":true,\"n\":1}]", 1u) < 0);
    assert(json_parser_ast_array_string_eq_at((uint8_t*)"[\"Ada\",\"Bob\"]", 1u, (uint8_t*)"Bob") == 1);
    assert(json_parser_ast_array_bool_at((uint8_t*)"[false,true]", 1u) == 1);
    assert(json_parser_ast_array_is_null_at((uint8_t*)"[1,null,3]", 1u) == 1);
    assert(json_parser_ast_array_i64_at((uint8_t*)"[4,5,6]", 3u, &missing_num) == 0);
    assert(json_parser_ast_object_value_kind_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 3u) < 0);
    assert(json_parser_ast_array_kind_at((uint8_t*)"[1,\"Ada\",false,null,[2],{\"ok\":true}]", 6u) < 0);
    assert(json_parser_ast_field_u64((uint8_t*)dom_doc, (uint8_t*)"big", &big_num) == 1);
    assert(json_parser_ast_field_i64((uint8_t*)dom_doc, (uint8_t*)"missing", &first_num) == 0);
    assert(first_num == 123);
    assert(second_num == -2);
    assert(object_second == 2);
    assert(array_root_second == 5);
    object_array_kind = json_parser_ast_object_value_kind_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 1u);
    object_object_kind = json_parser_ast_object_value_kind_at((uint8_t*)"{\"name\":\"Ada\",\"items\":[4,5],\"meta\":{\"ok\":true}}", 2u);
    array_string_kind = json_parser_ast_array_kind_at((uint8_t*)"[1,\"Ada\",false,null,[2],{\"ok\":true}]", 1u);
    array_array_kind = json_parser_ast_array_kind_at((uint8_t*)"[1,\"Ada\",false,null,[2],{\"ok\":true}]", 4u);
    array_object_kind = json_parser_ast_array_kind_at((uint8_t*)"[1,\"Ada\",false,null,[2],{\"ok\":true}]", 5u);
    object_bool = json_parser_ast_object_field_bool_at((uint8_t*)"{\"name\":\"Ada\",\"ok\":true,\"none\":null}", 1u);
    object_null = json_parser_ast_object_field_is_null_at((uint8_t*)"{\"name\":\"Ada\",\"ok\":true,\"none\":null}", 2u);
    array_bool = json_parser_ast_array_bool_at((uint8_t*)"[false,true]", 1u);
    array_null = json_parser_ast_array_is_null_at((uint8_t*)"[1,null,3]", 1u);
    assert(object_array_kind == 4);
    assert(object_object_kind == 5);
    assert(array_string_kind == 3);
    assert(array_array_kind == 4);
    assert(array_object_kind == 5);
    assert(object_bool == 1);
    assert(object_null == 1);
    assert(array_bool == 1);
    assert(array_null == 1);
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
