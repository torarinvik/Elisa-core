#include <stdio.h>
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
    Vec2i kept = vec2i_keep_left(a, b);
    printf("sum=(%d,%d) kept=(%d,%d) sizeof(Vec2i)=%zu\n", sum.x, sum.y, kept.x, kept.y, sizeof(Vec2i));
    return 0;
}
