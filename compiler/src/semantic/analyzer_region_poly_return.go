package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// functionReturnsRegionAllocatedValue reports whether any value-returning path of fn hands back a
// value whose result is a `new[auto]` allocation, or a call to an already-region-polymorphic
// function. Walks only statement bodies (not lambda expression bodies), so a `return` inside a
// nested closure is correctly excluded.
func (a *Analyzer) functionReturnsRegionAllocatedValue(fn *ast.FuncDecl) bool {
	found := false
	// retCarriesRegionStorage gates the struct-literal-field rule below: it only fires when the
	// return type can actually hold region-backed storage (a container, view, or struct with
	// such a field), so a function returning a scalar field copy is never mis-classified.
	retCarriesRegionStorage := false
	if fn != nil {
		if ft := a.funcTypeForRegionPoly(fn); ft != nil {
			retCarriesRegionStorage = typeCarriesRegionStorage(ft.Return)
		}
	}
	// Locals fed by region-allocated values (a `result` accumulated across branches from
	// region-polymorphic calls / bare region-backed constructors): returning one is returning a
	// region-allocated value — the same local-tracking functionBuildsAndReturnsLocalContainer
	// does for container literals.
	regionLocals := map[string]bool{}
	// regiony extends exprResultIsRegionAllocated with local knowledge: an ident naming a
	// region-fed local, a struct literal wrapping one, or a region-less container literal
	// (allocated in the inferred region) all carry region-allocated data.
	// rootedInRegionLocal reports whether an argument hands the callee a region-fed
	// local's storage: the local itself, a field/element of it, or its address. A call
	// that receives such an argument can return a value sharing that storage — a darray
	// field copied out (`return E.X(p.items)`) copies the HEADER, not the buffer.
	rootedInRegionLocal := func(arg ast.Expr) bool {
		for {
			switch e := unwrapParenForRegionPoly(arg).(type) {
			case *ast.AddrOfExpr:
				arg = e.Operand
				continue
			case *ast.MoveExpr:
				arg = e.Operand
				continue
			}
			break
		}
		root := rootIdentExpr(unwrapParenForRegionPoly(arg))
		return root != nil && regionLocals[root.Name]
	}
	// callSharesRegionLocal: an ordinary call whose receiver or argument is rooted in a
	// region-fed local may return that local's storage. The callee's own classification
	// cannot see this — the callee allocates nothing (`def f(p: P&) -> E: return
	// E.X(p.items)`) — so without this rule the caller freed its auto region at `ret`
	// while the returned payload still pointed into it: element reads came back zero, a
	// silent wrong answer in safe code. Gated on a region-carrying return type; a false
	// match only threads the caller's region, which is sound.
	// viewElementLocals: locals whose every declaration spells `darray[sview]` (and that no
	// tuple bind or parameter also names). Such a container's own storage holds only view
	// HEADERS — no bytes a view can point into — so when nothing in this function's return
	// type can hold a `darray[sview]` header, a reference or a generic view, a callee handed
	// that container can return copies of its elements, never its storage. The
	// element's backing is whatever the pushed view already pointed at, which this call does
	// not change; treating the container as sharing its region with the result made every
	// caller of a `path_text(names, table)`-style helper adopt a region it never touched.
	viewElementLocals := map[string]bool{}
	if fn != nil && retCarriesRegionStorage {
		if ft := a.funcTypeForRegionPoly(fn); ft != nil {
			blocked := map[string]bool{}
			for _, param := range fn.Params {
				blocked[param.Name] = true
			}
			fillMayAdoptWalk(reflect.ValueOf(fn.Body), func(node any) {
				switch s := node.(type) {
				case *ast.VarDeclStmt:
					if elem, ok := headerOnlyDarrayElemName(s.Type); ok && !typeMayReachDarrayStorage(ft.Return, elem) {
						viewElementLocals[s.Name] = true
					} else {
						blocked[s.Name] = true
					}
				case *ast.TupleBindStmt:
					for _, nm := range s.Names {
						blocked[nm.Name] = true
					}
				}
			})
			for name := range blocked {
				delete(viewElementLocals, name)
			}
		}
	}
	callSharesRegionLocal := func(callee ast.Expr, args []ast.Expr) bool {
		if !retCarriesRegionStorage {
			return false
		}
		if len(viewElementLocals) > 0 {
			kept := make([]ast.Expr, 0, len(args))
			for _, arg := range args {
				inner := arg
				for {
					switch e := unwrapParenForRegionPoly(inner).(type) {
					case *ast.AddrOfExpr:
						inner = e.Operand
						continue
					case *ast.MoveExpr:
						inner = e.Operand
						continue
					}
					break
				}
				// The container itself, an element copy (`names[i]`) or a scalar field
				// (`names.count`): none of them is the container's storage.
				if id := rootIdentExpr(unwrapParenForRegionPoly(inner)); id != nil && viewElementLocals[id.Name] {
					continue
				}
				kept = append(kept, arg)
			}
			args = kept
		}
		if fe, ok := unwrapParenForRegionPoly(callee).(*ast.FieldExpr); ok && fe != nil && rootedInRegionLocal(fe.Object) {
			return true
		}
		for _, arg := range args {
			if arg != nil && rootedInRegionLocal(arg) {
				return true
			}
		}
		return false
	}
	var regiony func(value ast.Expr) bool
	regiony = func(value ast.Expr) bool {
		value = unwrapParenForRegionPoly(value)
		switch e := value.(type) {
		case *ast.Ident:
			return regionLocals[e.Name]
		case *ast.AddrOfExpr:
			// `r: P& = &p` makes `r` a way to reach `p`'s storage, so a later `f(r)`
			// shares it exactly as `f(&p)` does.
			if retCarriesRegionStorage && rootedInRegionLocal(e.Operand) {
				return true
			}
		case *ast.FieldExpr:
			// A BARE `return b.items`, where `b` is a region-fed local: the field's BUFFER lives
			// in `b`'s region, so this function must thread the caller's region exactly as the
			// struct-literal-field case below does. Without it the header copies out while the
			// backing is freed on return — `.count` reads fine, element reads segfault.
			//
			// This was previously excluded (the comment below said so), which left a SILENT UAF
			// in safe code: `def leak() -> darray[i64]: b: mutable Bag = Bag{items: []}; …;
			// return b.items` compiled clean and segfaulted at runtime, while both neighbouring
			// shapes — `return b` (whole struct) and `return xs` (a local darray) — were already
			// classified and worked.
			//
			// `retCarriesRegionStorage` is the same guard the struct-literal case relies on, and
			// it is what keeps a scalar field copy (`return b.value: i64`) unclassified: that
			// return type carries no region storage.
			if retCarriesRegionStorage {
				if root := rootIdentExpr(e); root != nil && regionLocals[root.Name] {
					return true
				}
			}
		case *ast.StructLitExpr:
			// Pre-analysis a call `f(&p)` parses as this node too.
			if callSharesRegionLocal(nil, e.Args) {
				return true
			}
			for _, arg := range e.Args {
				if arg == nil {
					continue
				}
				if regiony(arg) {
					return true
				}
				// A container field of a region-fed local as a struct-literal field carries
				// that local's region (the build-and-return-aggregate pattern:
				// `return File{errors: parser.errors}`, parser built by a region-poly ctor).
				// Restricted to a struct-literal field (not a bare `return b.field`) and to a
				// region-carrying return type, so a scalar field copy (`return b.value: i64`)
				// or a bare field return is unaffected. Without this the field's buffer dangles
				// after return — count reads fine, element reads segfault.
				if retCarriesRegionStorage {
					if fe, ok := unwrapParenForRegionPoly(arg).(*ast.FieldExpr); ok {
						if root := rootIdentExpr(fe); root != nil && regionLocals[root.Name] {
							return true
						}
					}
				}
			}
		case *ast.CallExpr:
			// An INLINE (non-packed) enum constructor whose payload carries region-allocated
			// data — `return Box.Full(tag, items)`, items a region-fed local. The value enum
			// embeds its payload, so the returned value holds the container's header while the
			// backing lives in this function's inferred region: exactly the struct-literal case
			// below it, and it must thread + adopt the caller's region the same way.
			//
			// Without this the auto region was freed at `ret` and the caller read the payload's
			// buffer after free — a SILENT wrong answer in safe code, not a crash: the darray
			// header is copied out by value so `.count` reads correctly and every ELEMENT reads
			// as zero. The struct spelling of the identical function (`return Box{tag: tag,
			// items: items}`) was already classified and worked, which is what makes this a bug
			// rather than a policy. Packed enums are excluded: a packed constructor is a handle
			// into a store, already covered by regionBackedEnumConstructor above.
			if callSharesRegionLocal(e.Func, e.Args) {
				return true
			}
			if retCarriesRegionStorage && a.callIsInlineEnumConstructor(e) {
				for _, arg := range e.Args {
					if arg == nil {
						continue
					}
					if regiony(arg) {
						return true
					}
					if fe, ok := unwrapParenForRegionPoly(arg).(*ast.FieldExpr); ok {
						if root := rootIdentExpr(fe); root != nil && regionLocals[root.Name] {
							return true
						}
					}
				}
			}
		case *ast.ListLitExpr:
			// A region-less container literal allocates into the inferred region — even an
			// empty `[]` seed that gets pushed into (the build-local-return shape).
			if e.Owner == nil {
				return true
			}
			for _, item := range e.Elems {
				if item != nil && regiony(item) {
					return true
				}
			}
		case *ast.ListComprehensionExpr:
			// A region-less comprehension (`[x for x in xs]`, list/dict/set) allocates its
			// result into the inferred region exactly like a container literal — returning one
			// makes the function region-polymorphic. The parser wraps such a body in a
			// synthesized auto region (functionBodyNeedsAutoRegion's ReturnStmt case) so the
			// comprehension has an active scope; classification here adopts that region into
			// the caller so the result outlives the call.
			if e.Owner == nil {
				return true
			}
		case *ast.QueryExpr:
			// An `each` query (`X{...} for each v in src`) builds a fresh darray in the
			// inferred region — the QueryExpr analogue of the list comprehension above. It
			// MUST classify region-poly: if the function also calls a region-poly callee,
			// wrapRegionPolyCallerBodies gives its body a synthesized LOCAL region, so the
			// query compiles — but without this classification the local region is never
			// adopted by the caller and dies at return, leaving the returned darray's buffer
			// dangling (count reads fine, element reads segfault).
			if e.Kind == ast.QueryExprEach && e.Owner == nil {
				return true
			}
		case *ast.TupleExpr:
			// Lowered grammar try-productions return (matched, committed, value) tuples;
			// a region-allocated value inside makes the whole return region-allocated.
			for _, item := range e.Elems {
				if item != nil && regiony(item) {
					return true
				}
			}
		case *ast.TernaryExpr:
			// docs/119 §4: a value `if` — either branch producing a region-allocated
			// value makes the whole conditional region-allocated.
			if regiony(e.Value) || regiony(e.Alt) {
				return true
			}
		case *ast.MatchExpr:
			// docs/119 §4: a match-expression — an arm whose tail value is
			// region-allocated makes the whole match region-allocated (arm bodies'
			// interior returns are seen by the statement walk below).
			for _, arm := range e.Arms {
				if len(arm.Body) == 0 {
					continue
				}
				if tail, ok := arm.Body[len(arm.Body)-1].(*ast.ExprStmt); ok && tail != nil && regiony(tail.Expr) {
					return true
				}
			}
		case *ast.ExprBlock:
			// docs/119 §2: a block expression's value is the block's result.
			if regiony(e.Value) {
				return true
			}
		}
		return a.exprResultIsRegionAllocated(value)
	}
	var collect func(stmts []ast.Stmt)
	// A binding's RHS can itself carry statements (a match-expression's arm bodies, a
	// block expression's leading statements — docs/119 §2/§4): the locals declared there
	// (e.g. the rebind-desugared `done_term` inside a `term = match …:` arm) feed the
	// value's tail, so collect must see them or the tail Ident never registers.
	var collectValueStmts func(value ast.Expr)
	collectValueStmts = func(value ast.Expr) {
		switch e := unwrapParenForRegionPoly(value).(type) {
		case *ast.MatchExpr:
			for _, arm := range e.Arms {
				collect(arm.Body)
			}
		case *ast.ExprBlock:
			collect(e.Stmts)
			collectValueStmts(e.Value)
		case *ast.TernaryExpr:
			collectValueStmts(e.Value)
			collectValueStmts(e.Alt)
		}
	}
	collect = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.VarDeclStmt:
				if s.Value != nil {
					collectValueStmts(s.Value)
					if regiony(s.Value) {
						regionLocals[s.Name] = true
					}
				}
			case *ast.AssignStmt:
				if s.Value != nil {
					collectValueStmts(s.Value)
				}
				// The target's ROOT local, not just a bare ident: storing a region-allocated
				// value INTO a field or element of a local makes that local carry region-backed
				// storage, exactly as if it had been built by a struct literal.
				//
				//   board: mutable Board = zeroed
				//   board.cells <- zeros(n)     # <- this
				//   return board
				//
				// Without the FieldExpr case this compiled to a per-call arena that was
				// arena_free'd before `ret`, so the returned struct's buffer pointer dangled:
				// a silent use-after-free in safe code, `.count` reading fine and element reads
				// segfaulting. The struct-literal spelling of the same function
				// (`return Board{cells: zeros(n)}`) was already classified and worked — that
				// divergence is what makes this a bug rather than a policy.
				if s.Value != nil && regiony(s.Value) {
					if root := rootIdentExpr(s.Target); root != nil {
						regionLocals[root.Name] = true
					}
				}
			case *ast.TupleBindStmt:
				// A multi-target rebind/tuple bind whose RHS is region-allocated feeds
				// every bound name (the temps a `rebind a, b = call()` destructures into).
				if s.Value != nil {
					collectValueStmts(s.Value)
					if regiony(s.Value) {
						for _, nm := range s.Names {
							regionLocals[nm.Name] = true
						}
					}
				}
			case *ast.IfStmt:
				collect(s.Then)
				for _, elif := range s.Elifs {
					collect(elif.Body)
				}
				collect(s.Else)
			case *ast.WhileStmt:
				collect(s.Body)
			case *ast.ForStmt:
				collect(s.Body)
			case *ast.IterForStmt:
				collect(s.Body)
			case *ast.ScopeStmt:
				collect(s.Body)
			case *ast.CanStmt:
				collect(s.Body)
			case *ast.InStoreStmt:
				collect(s.Body)
			case *ast.RegionStmt:
				// The parser wraps allocating bodies in a synthesized `__auto_*` region; the
				// values inside are exactly the ones a region-polymorphic caller adopts, so
				// the classifier must see through it. Explicit named regions stay opaque
				// (their values die with the region — returning them is a separate error).
				if isCompilerGeneratedAutoRegionStmt(s) {
					collect(s.Body)
				}
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					collect(arm.Body)
				}
			}
		}
	}
	if fn != nil {
		// Fixpoint: locals feed locals (`d := ctor; decls := [d]; return Result{decls}`).
		for {
			before := len(regionLocals)
			collect(fn.Body)
			if len(regionLocals) == before {
				break
			}
		}
	}
	var walk func(stmts []ast.Stmt)
	check := func(value ast.Expr) {
		if value != nil && regiony(value) {
			found = true
		}
	}
	walk = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ReturnStmt:
				check(s.Value)
				// docs/119 §4: a returned match-expression carries statement arm
				// bodies — walk them so an arm's interior `return <alloc>` classifies.
				if me, ok := unwrapParenForRegionPoly(s.Value).(*ast.MatchExpr); ok && me != nil {
					for _, arm := range me.Arms {
						walk(arm.Body)
					}
				}
			case *ast.IfStmt:
				walk(s.Then)
				for _, elif := range s.Elifs {
					walk(elif.Body)
				}
				walk(s.Else)
			case *ast.WhileStmt:
				walk(s.Body)
			case *ast.ForStmt:
				walk(s.Body)
			case *ast.IterForStmt:
				walk(s.Body)
			case *ast.ParallelForStmt:
				walk(s.Body)
			case *ast.ScopeStmt:
				walk(s.Body)
			case *ast.MatchStmt:
				for _, arm := range s.Arms {
					walk(arm.Body)
				}
			case *ast.CanStmt:
				walk(s.Body)
			case *ast.RegionStmt:
				walk(s.Body)
			case *ast.InStoreStmt:
				walk(s.Body)
			}
		}
	}
	walk(fn.Body)
	return found
}
