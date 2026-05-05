#include <stddef.h>

/*
 * The self-hosted lexer object currently pulls in a few runtime support symbols
 * from the shared elisacore runtime even though the lexer harness never calls
 * those code paths. These tiny shims satisfy the linker for benchmark and
 * generated-header harness binaries on macOS.
 */

void *stderr = NULL;

void *frontend_lexer_stub_va_copy(void *args) __asm__("va_copy");
void *frontend_lexer_stub_va_copy(void *args) {
    return args;
}

void frontend_lexer_stub_va_end(void *args) __asm__("va_end");
void frontend_lexer_stub_va_end(void *args) {
    (void)args;
}
