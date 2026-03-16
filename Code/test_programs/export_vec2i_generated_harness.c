#include "export_vec2i.h"
#include <assert.h>

int main(void) {
    Vec2i a = {1, 2};
    Vec2i b = {3, 4};

    assert(ctx_seed == 7);

    Vec2i sum = vec2i_add(a, b);
    assert(sum.x == 4);
    assert(sum.y == 6);

    Vec2i kept = vec2i_keep_left(a, b);
    assert(kept.x == 1);
    assert(kept.y == 2);

    return 0;
}