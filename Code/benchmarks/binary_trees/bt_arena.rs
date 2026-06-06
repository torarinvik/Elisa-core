// Index-arena Rust: nodes in one Vec, children as indices; arena dropped per tree (bulk free).
struct Node { left: i32, right: i32 } // left < 0 => leaf
fn make(a: &mut Vec<Node>, d: i64) -> i32 {
    if d <= 0 { a.push(Node{left:-1,right:-1}); (a.len()-1) as i32 }
    else { let l = make(a,d-1); let r = make(a,d-1); a.push(Node{left:l,right:r}); (a.len()-1) as i32 }
}
fn check(a: &Vec<Node>, i: i32) -> i64 {
    let n = &a[i as usize];
    if n.left < 0 { 1 } else { 1 + check(a, n.left) + check(a, n.right) }
}
fn main() {
    let (max_depth, min_depth) = (18i64, 4i64);
    let mut total;
    { let mut a = Vec::new(); let s = make(&mut a, max_depth+1); total = check(&a, s); }
    let mut la = Vec::new(); let ll = make(&mut la, max_depth);
    let mut d = min_depth;
    while d <= max_depth {
        let iters = 1i64 << (max_depth - d + min_depth);
        let mut s = 0i64;
        for _ in 0..iters { let mut a = Vec::new(); let t = make(&mut a, d); s += check(&a, t); }
        total += s;
        d += 2;
    }
    total += check(&la, ll);
    println!("binary-trees(18) check total: {}", total);
}
