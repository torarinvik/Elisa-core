#include <assert.h>
#include <stdint.h>

typedef struct Vec2i {
    int32_t x;
    int32_t y;
} Vec2i;

Vec2i vec2i_add(Vec2i left, Vec2i right);
Vec2i vec2i_keep_left(Vec2i left, Vec2i right);

int main(void) {
    Vec2i a = {1, 2};
    Vec2i b = {3, 4};

    Vec2i sum = vec2i_add(a, b);
    assert(sum.x == 4);
    assert(sum.y == 6);

    Vec2i kept = vec2i_keep_left(a, b);
    assert(kept.x == 1);
    assert(kept.y == 2);

    return 0;
}
