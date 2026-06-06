// Idiomatic C++: tagged tree with unique_ptr (per-node alloc/free).
#include <cstdio>
#include <memory>
struct Tree { std::unique_ptr<Tree> l, r; };
static std::unique_ptr<Tree> make(long depth){
    auto t = std::make_unique<Tree>();
    if(depth>0){ t->l=make(depth-1); t->r=make(depth-1); }
    return t;
}
static long check(const Tree* n){ if(!n->l) return 1; return 1+check(n->l.get())+check(n->r.get()); }
int main(){
    long max_depth=18, min_depth=4, total=0;
    { auto s=make(max_depth+1); total+=check(s.get()); }
    auto longLived=make(max_depth);
    for(long d=min_depth; d<=max_depth; d+=2){
        long iters=1L<<(max_depth-d+min_depth), sum=0;
        for(long i=0;i<iters;i++){ auto t=make(d); sum+=check(t.get()); }
        total+=sum;
    }
    total+=check(longLived.get());
    printf("binary-trees(18) check total: %ld\n", total);
    return 0;
}
