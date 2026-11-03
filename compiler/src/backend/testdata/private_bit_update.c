#include <stdint.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>
extern int64_t update(const int64_t *,uint8_t *);
int main(void){
 size_t page=(size_t)sysconf(_SC_PAGESIZE);
 uint8_t *mapping=mmap(NULL,page*2,PROT_READ|PROT_WRITE,MAP_PRIVATE|MAP_ANON,-1,0);
 if(mapping==MAP_FAILED||mprotect(mapping+page,page,PROT_NONE))return 2;
 int64_t *input=(int64_t *)(mapping+page-32);
 for(unsigned pattern=0;pattern<16;++pattern){
  for(int invalid=-1;invalid<4;++invalid){
   uint8_t output[24],expected[24];memset(output,0xa5,24);memcpy(expected,output,24);
   for(unsigned i=0;i<4;++i)input[i]=(pattern>>i)&1;
   if(invalid>=0)input[invalid]=pattern&1?INT64_MAX:-1;
   int64_t result=update(input,output+1);
   if(invalid<0){for(unsigned i=0;i<4;++i){expected[1+2*i]=(uint8_t)input[i];expected[2+2*i]=0;}if(result!=8)return 1;}
   else if(result!=-1)return 1;
   if(memcmp(output,expected,24))return 1;
  }
 }
 return munmap(mapping,page*2)!=0;
}
