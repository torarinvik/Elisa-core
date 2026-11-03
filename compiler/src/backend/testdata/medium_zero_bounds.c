#include <sys/mman.h>
#include <unistd.h>
#include <string.h>
#include <stdint.h>
#include <stdio.h>
extern void test_1024(void *);
extern void test_2048(void *);
static int check(unsigned char *p, size_t capacity, size_t offset, size_t n, void (*fill)(void *)) {
  memset(p, 0xa5, capacity);
  fill(p + offset);
  for (size_t i = 0; i < capacity; ++i)
    if (p[i] != (i >= offset && i < offset + n ? 0 : 0xa5)) return 1;
  return 0;
}
int main(void) {
  size_t page = (size_t)sysconf(_SC_PAGESIZE);
  unsigned char *m = mmap(0, 3 * page, PROT_NONE, MAP_PRIVATE | MAP_ANON, -1, 0);
  if (m == MAP_FAILED || mprotect(m + page, page, PROT_READ | PROT_WRITE)) return 2;
  int failed = 0;
  for (size_t off = 0; off <= 64; off += 8) {
    failed |= check(m + page, page, off, 1024, test_1024);
    failed |= check(m + page, page, page - 1024 - off, 1024, test_1024);
    failed |= check(m + page, page, off, 2048, test_2048);
    failed |= check(m + page, page, page - 2048 - off, 2048, test_2048);
  }
  munmap(m, 3 * page);
  if (failed) fprintf(stderr, "inline fill wrote outside its range\n");
  return failed;
}
