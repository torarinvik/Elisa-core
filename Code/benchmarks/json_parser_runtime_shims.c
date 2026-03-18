#include <stdarg.h>
#include <stdio.h>

#if defined(__APPLE__) && defined(__aarch64__)
__asm__(".globl va_copy\n"
        "va_copy = __builtin_va_copy\n");
#endif

FILE* stderr = 0;
