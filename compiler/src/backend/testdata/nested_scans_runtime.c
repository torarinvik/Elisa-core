#include <stdint.h>
#include <stdio.h>
#include <string.h>
extern int64_t scan(int64_t *, int64_t *, uint64_t, uint64_t, uint64_t);
static int64_t reference(int64_t *a, int64_t *b, uint64_t n, uint64_t outer, uint64_t stop) {
 if (!n) return 0;
 for (uint64_t j=0; j<outer; ++j) {
  for (uint64_t i=0; i<n; ++i) {
   if (i==stop) return i;
   if (a[i]>0 && b[i]<0) b[i]=a[i];
  }
 }
 return outer;
}
int main(void) {
 for (uint64_t n=0;n<=16;++n) for (uint64_t outer=1;outer<=4;++outer)
 for (uint64_t stop=0;stop<=17;++stop) {
  int64_t a[16], b[16], expected[16];
  for(int i=0;i<16;++i){a[i]=(i%3)?i+1:-i-1;b[i]=(i%4)?-i-1:i+1;}
  memcpy(expected,b,sizeof(b));
  int64_t e=reference(a,expected,n,outer,stop), actual=scan(a,b,n,outer,stop);
  if(e!=actual||memcmp(b,expected,sizeof(b))) {fprintf(stderr,"mismatch n=%llu outer=%llu stop=%llu\n",(unsigned long long)n,(unsigned long long)outer,(unsigned long long)stop);return 1;}
 }
 return 0;
}
