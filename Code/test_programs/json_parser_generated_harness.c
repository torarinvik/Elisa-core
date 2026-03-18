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

    return 0;
}
