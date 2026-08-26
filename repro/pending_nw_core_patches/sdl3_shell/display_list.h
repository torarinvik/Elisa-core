/* Decoding the nw-core display list.
 *
 * The C twin of shells/web/display-list.js and test/display_decode.py. All three
 * decode the same 40-byte fixed command record, and keeping them
 * line-comparable is how they stay in agreement.
 *
 *   [magic u32][version u32][count u32][pool_len u32][commands...][pool bytes]
 *
 * A command is 40 bytes: kind at +0, color at +4, six f32 at +8..+32, then two
 * u32 (ref, len) at +32 and +36. For TEXT and MARKER, `ref` is a byte offset
 * into the pool and the low 24 bits of `len` are the string length; TEXT packs
 * its anchor into the high 8 bits.
 */
#ifndef NW_DISPLAY_LIST_H
#define NW_DISPLAY_LIST_H

#include <stdint.h>
#include <string.h>

#define NW_DL_MAGIC    1313426254u
#define NW_DL_VERSION  1u
#define NW_DL_CMD_SIZE 40u

#define NW_REFERENCE_WIDTH  912.0f
#define NW_REFERENCE_HEIGHT 684.0f

enum nw_kind {
    NW_RECT = 1, NW_BORDERED_RECT = 2, NW_BOX = 3, NW_CIRCLE = 4, NW_ARC = 5,
    NW_LINE = 6, NW_TRIANGLE = 7, NW_POLYGON = 8, NW_POLY_POINT = 9,
    NW_SPRITE = 10, NW_TEXT = 11, NW_VERDICT = 12, NW_SOUND = 13, NW_MARKER = 14
};

typedef struct {
    uint8_t  kind;
    uint32_t color;
    float    a, b, c, d, e, f;
    uint32_t ref, len;
} nw_cmd;

typedef struct {
    const nw_cmd *commands;      /* borrowed: points into the caller's buffer */
    uint32_t      count;
    const uint8_t *pool;
    uint32_t      pool_len;
} nw_frame;

static inline uint32_t nw_read_u32(const uint8_t *at) {
    uint32_t v;                       /* the encoder writes little-endian */
    memcpy(&v, at, 4);
    return v;
}

static inline float nw_read_f32(const uint8_t *at) {
    float v;
    memcpy(&v, at, 4);
    return v;
}

/* Decodes in place: `out->commands` is a view into `bytes`, valid as long as it
 * is. Returns 0 on a bad header rather than trusting the buffer. `scratch` must
 * hold at least `count` commands; the caller owns it. */
static inline int nw_decode(const uint8_t *bytes, size_t length, nw_cmd *scratch,
                            size_t scratch_count, nw_frame *out) {
    if (length < 16) return 0;
    if (nw_read_u32(bytes) != NW_DL_MAGIC) return 0;
    if (nw_read_u32(bytes + 4) != NW_DL_VERSION) return 0;
    uint32_t count = nw_read_u32(bytes + 8);
    uint32_t pool_len = nw_read_u32(bytes + 12);
    if (count > scratch_count) return 0;
    if (16 + (size_t)count * NW_DL_CMD_SIZE + pool_len > length) return 0;

    for (uint32_t i = 0; i < count; i++) {
        const uint8_t *at = bytes + 16 + (size_t)i * NW_DL_CMD_SIZE;
        scratch[i].kind  = at[0];
        scratch[i].color = nw_read_u32(at + 4);
        scratch[i].a = nw_read_f32(at + 8);
        scratch[i].b = nw_read_f32(at + 12);
        scratch[i].c = nw_read_f32(at + 16);
        scratch[i].d = nw_read_f32(at + 20);
        scratch[i].e = nw_read_f32(at + 24);
        scratch[i].f = nw_read_f32(at + 28);
        scratch[i].ref = nw_read_u32(at + 32);
        scratch[i].len = nw_read_u32(at + 36);
    }
    out->commands = scratch;
    out->count = count;
    out->pool = bytes + 16 + (size_t)count * NW_DL_CMD_SIZE;
    out->pool_len = pool_len;
    return 1;
}

/* Text/marker payload. Returns the length and points `text` at pool bytes --
 * NOT NUL-terminated, so callers copy before handing it to a C string API. */
static inline uint32_t nw_text_of(const nw_frame *frame, const nw_cmd *cmd,
                                  const uint8_t **text) {
    uint32_t len = cmd->len & 0xffffffu;
    if (cmd->ref + len > frame->pool_len) { *text = NULL; return 0; }
    *text = frame->pool + cmd->ref;
    return len;
}

#define NW_ANCHOR_OF(cmd) (((cmd)->len >> 24) & 0xffu)

#endif
