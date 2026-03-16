#include <stdint.h>

typedef struct Vec2i {
    int32_t x;
    int32_t y;
} Vec2i;

Vec2i vec2i_add_ref(Vec2i left, Vec2i right) {
    Vec2i result = {left.x + right.x, left.y + right.y};
    return result;
}
