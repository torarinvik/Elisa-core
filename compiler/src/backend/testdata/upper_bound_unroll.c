#include <stdint.h>
#include <string.h>
extern uint64_t bounded(uint64_t *,uint64_t);
extern uint64_t exact(uint64_t *);
int main(void){
 uint64_t initial[8],data[8],reference[8];
 for(unsigned pattern=0;pattern<64;++pattern){
  for(unsigned i=0;i<8;++i)initial[i]=pattern&1?UINT64_MAX-i-pattern:pattern*7+i;
  for(unsigned n=0;n<=8;++n){
   memcpy(data,initial,sizeof data);memcpy(reference,initial,sizeof reference);uint64_t expected=0;
   if(n>6)expected=UINT64_MAX;else for(unsigned i=0;i<n;++i){expected+=reference[i];++reference[i];}
   if(bounded(data,n)!=expected||memcmp(data,reference,sizeof data))return 1;
  }
  memcpy(data,initial,sizeof data);memcpy(reference,initial,sizeof reference);uint64_t expected=0;
  for(unsigned i=0;i<6;++i){expected+=reference[i];++reference[i];}
  if(exact(data)!=expected||memcmp(data,reference,sizeof data))return 1;
 }
 return 0;
}
