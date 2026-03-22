#include <stdarg.h>
#include <stdio.h>

#if defined(__APPLE__) && defined(__aarch64__)
__asm__(".globl va_copy\n"
        "va_copy = __builtin_va_copy\n");
#endif

/*
 * On macOS, <stdio.h> exposes stderr as a macro alias. Undefine it so this
 * shim exports the raw symbol name expected by generated O0 test harnesses.
 */
#ifdef stderr
#undef stderr
#endif

void *stderr = 0;
