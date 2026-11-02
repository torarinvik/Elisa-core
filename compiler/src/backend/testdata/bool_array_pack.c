#include <stdint.h>
#include <stdbool.h>
#include <stdio.h>
extern uint64_t roundtrip2(uint64_t,uint64_t,bool);
extern uint64_t roundtrip4(uint64_t,uint64_t,bool);
extern uint64_t roundtrip16(uint64_t,uint64_t,bool);
extern uint64_t roundtrip64(uint64_t,uint64_t,bool);
int main(void) {
 uint64_t (*functions[])(uint64_t,uint64_t,bool)={roundtrip2,roundtrip4,roundtrip16,roundtrip64};
 unsigned lengths[]={2,4,16,64};uint64_t state=UINT64_C(0x97ef3211d367afa9);unsigned cases=0;
 for(unsigned f=0;f<4;f++)for(unsigned sample=0;sample<128;sample++){
  state^=state<<13;state^=state>>7;state^=state<<17;
  uint64_t mask=lengths[f]==64?UINT64_MAX:(UINT64_C(1)<<lengths[f])-1;
  for(unsigned j=0;j<lengths[f]+3;j++)for(unsigned value=0;value<2;value++){
   uint64_t index=j==lengths[f]+2?UINT64_MAX:j;
   uint64_t expected=state;
   if(index<lengths[f])expected=((state&mask)&~(UINT64_C(1)<<index))|((uint64_t)value<<index);
   uint64_t got=functions[f](state,index,value!=0);cases++;
   if(got!=expected){fprintf(stderr,"n=%u index=%llu value=%u: mismatch\n",lengths[f],(unsigned long long)index,value);return 1;}
  }
 }
 printf("passed %u Boolean array round trips\n",cases);return 0;
}
