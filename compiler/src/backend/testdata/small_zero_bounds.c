#include <sys/mman.h>
#include <unistd.h>
#include <string.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#ifndef FILL_BYTE
#define FILL_BYTE 0
#endif
extern void test_smallzero(void *, uint64_t);
static void check(unsigned char *base,size_t cap,size_t off,size_t n) {
 memset(base,0x5a,cap);test_smallzero(base+off,n);
 for(size_t i=0;i<cap;i++)if(base[i]!=(i>=off&&i<off+n?FILL_BYTE:0x5a)){fprintf(stderr,"bad length=%zu offset=%zu byte=%zu\n",n,off,i);exit(1);}
}
int main(void){
 size_t p=(size_t)sysconf(_SC_PAGESIZE);
 unsigned char *m=mmap(0,3*p,PROT_NONE,MAP_PRIVATE|MAP_ANON,-1,0);
 if(m==MAP_FAILED||mprotect(m+p,p,PROT_READ|PROT_WRITE))return 2;
 test_smallzero(0,0);test_smallzero(m,0);
 unsigned cases=2;
 for(size_t n=0;n<=256;n++){
  check(m+p,p,0,n);check(m+p,p,p-n,n);cases+=2;
  for(size_t a=0;a<32;a++){check(m+p,p,64+a,n);cases++;}
 }
 munmap(m,3*p);printf("passed %u byte-fill boundary/alignment cases\n",cases);
}
