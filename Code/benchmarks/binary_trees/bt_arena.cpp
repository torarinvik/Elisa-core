// Arena C++: bump-allocate nodes from a per-tree arena, bulk-freed (matches Elisa's region model).
#include <cstdio>
#include <cstdlib>
#include <vector>
struct Tree { Tree* l; Tree* r; };
struct Arena {
    std::vector<char*> blocks; size_t off=0, cap=0; char* cur=nullptr;
    static const size_t BLK = 1<<20;
    Tree* alloc(){
        if(off+sizeof(Tree)>cap){ cur=(char*)malloc(BLK); blocks.push_back(cur); cap=BLK; off=0; }
        Tree* t=(Tree*)(cur+off); off+=sizeof(Tree); return t;
    }
    ~Arena(){ for(char* b:blocks) free(b); }
};
static Tree* make(Arena& a, long depth){
    Tree* t=a.alloc();
    if(depth>0){ t->l=make(a,depth-1); t->r=make(a,depth-1); } else { t->l=nullptr; t->r=nullptr; }
    return t;
}
static long check(const Tree* n){ if(!n->l) return 1; return 1+check(n->l)+check(n->r); }
int main(){
    long max_depth=18, min_depth=4, total=0;
    { Arena a; auto s=make(a,max_depth+1); total+=check(s); }
    Arena longArena; auto longLived=make(longArena,max_depth);
    for(long d=min_depth; d<=max_depth; d+=2){
        long iters=1L<<(max_depth-d+min_depth), sum=0;
        for(long i=0;i<iters;i++){ Arena a; auto t=make(a,d); sum+=check(t); }
        total+=sum;
    }
    total+=check(longLived);
    printf("binary-trees(18) check total: %ld\n", total);
    return 0;
}
