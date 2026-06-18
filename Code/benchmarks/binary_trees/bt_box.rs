enum Tree { Leaf, Node(Box<Tree>, Box<Tree>) }
fn make(d: i64) -> Box<Tree> {
    if d <= 0 { Box::new(Tree::Leaf) } else { Box::new(Tree::Node(make(d-1), make(d-1))) }
}
fn check(t: &Tree) -> i64 {
    match t { Tree::Leaf => 1, Tree::Node(l, r) => 1 + check(l) + check(r) }
}
fn main() {
    let (max_depth, min_depth) = (18, 4);
    let mut total = check(&make(max_depth + 1));
    let long_lived = make(max_depth);
    let mut d = min_depth;
    while d <= max_depth {
        let iters = 1 << (max_depth - d + min_depth);
        let mut s = 0;
        for _ in 0..iters { s += check(&make(d)); }
        total += s;
        d += 2;
    }
    total += check(&long_lived);
    println!("binary-trees(18) check total: {}", total);
}
