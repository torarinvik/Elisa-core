package semantic

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"iter"

	"elisacore/src/ast"
	"elisacore/src/incremental"
)

const primitiveOwnerCandidateLimit = 16384
const primitiveOwnerPointerCheckNodeLimit = 65536

// Bound namespace and owner-key construction plus retained-local name hashing
// during buffered discovery. Over-budget discovery reuses its prefix and
// streams the suffix.
const primitiveOwnerCandidateWorkBudget = 8 * 1024 * 1024
const primitiveOwnerCandidateNameBytes = len("primitive-v1:") + sha256.Size*2
const primitiveOwnerLocalNameWorkOverhead = 256

type primitiveOwnerCandidate struct {
	function      *ast.FuncDecl
	qualifiedName string // populated only when lexical-read auditing streams candidates
	name          string // streamed records reuse the name already encoded for retention lookup
	identity      [32]byte
	retained      bool
}

// primitiveOwnerCandidateRecord is the bounded inventory representation. Its
// 64-bit pointer, 256-bit identity, and retained bit occupy about 48 bytes on
// 64-bit targets; it retains no qualified-name or key strings.
type primitiveOwnerCandidateRecord struct {
	function *ast.FuncDecl
	identity [32]byte
	retained bool
}

// The production admission maps use AST pointers as keys. Keep the inventory
// invocation-local, bound its work, and fail closed on repeated syntax nodes.
type primitiveOwnerPointerInventory struct {
	functions map[*ast.FuncDecl]struct{}
	locals    map[*ast.VarDeclStmt]struct{}
	visited   int
}

func (inventory *primitiveOwnerPointerInventory) add(fn *ast.FuncDecl) bool {
	if inventory == nil || fn == nil || !inventory.visitNode() {
		return false
	}
	if inventory.functions == nil {
		inventory.functions = make(map[*ast.FuncDecl]struct{})
	}
	if _, exists := inventory.functions[fn]; exists {
		return false
	}
	inventory.functions[fn] = struct{}{}
	for _, stmt := range fn.Body {
		if !inventory.visitNode() {
			return false
		}
		local, ok := stmt.(*ast.VarDeclStmt)
		if !ok || local == nil {
			continue
		}
		if inventory.locals == nil {
			inventory.locals = make(map[*ast.VarDeclStmt]struct{})
		}
		if _, exists := inventory.locals[local]; exists {
			return false
		}
		inventory.locals[local] = struct{}{}
	}
	return true
}

func (inventory *primitiveOwnerPointerInventory) visitNode() bool {
	if inventory == nil || inventory.visited >= primitiveOwnerPointerCheckNodeLimit {
		return false
	}
	inventory.visited++
	return true
}

// scanDeclarations covers every declaration container from which semantic
// analysis can reach a FuncDecl, including containers that owner admission
// does not currently cache. A repeated pointer across those roles is unsafe
// even when one occurrence would not become a candidate.
func (inventory *primitiveOwnerPointerInventory) scanDeclarations(decls []ast.Decl, depth int) bool {
	if depth > 128 {
		return false
	}
	for _, decl := range decls {
		switch node := decl.(type) {
		case *ast.NamespaceDecl:
			if node == nil || !inventory.visitNode() || !inventory.scanDeclarations(node.Decls, depth+1) {
				return false
			}
		case *ast.StaticIfDecl:
			if node == nil || !inventory.visitNode() || !inventory.scanDeclarations(node.Then, depth+1) {
				return false
			}
			for _, clause := range node.Elifs {
				if !inventory.visitNode() || !inventory.scanDeclarations(clause.Body, depth+1) {
					return false
				}
			}
			if !inventory.scanDeclarations(node.Else, depth+1) {
				return false
			}
		case *ast.ImplDecl:
			if node == nil || !inventory.visitNode() {
				return false
			}
			for _, member := range node.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					if !inventory.add(fn) {
						return false
					}
				} else if !inventory.visitNode() {
					return false
				}
			}
		case *ast.InterfaceDecl:
			if node == nil || !inventory.visitNode() {
				return false
			}
			for _, member := range node.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					if !inventory.add(fn) {
						return false
					}
				} else if !inventory.visitNode() {
					return false
				}
			}
		case *ast.FuncDecl:
			if node == nil {
				if !inventory.visitNode() {
					return false
				}
			} else if !inventory.add(node) {
				return false
			}
		default:
			if !inventory.visitNode() {
				return false
			}
		}
	}
	return true
}

// walkPrimitiveOwnerDeclarations preserves declaration occurrence order,
// including aliased AST pointers in distinct namespaces. Analyze's admission
// layer must validate pointer uniqueness before using candidates.
func walkPrimitiveOwnerDeclarations(decls []ast.Decl, namespace string, depth int, originBytes int, preparedBytes *int, skipCandidates *int, yield func(*ast.FuncDecl, string) bool) bool {
	if depth > 128 {
		return true
	}
	for _, decl := range decls {
		if nested, ok := decl.(*ast.NamespaceDecl); ok {
			if len(nested.Name) > primitiveSourceByteBudget-len(namespace)-1 {
				continue
			}
			namespaceBytes := len(namespace) + len(nested.Name)
			if namespace != "" {
				namespaceBytes++
			}
			if preparedBytes != nil && !reservePrimitiveOwnerCandidateBytes(preparedBytes, namespaceBytes, primitiveOwnerCandidateWorkBudget) {
				return false
			}
			if !walkPrimitiveOwnerDeclarations(nested.Decls, joinQualifiedName(namespace, nested.Name), depth+1, originBytes, preparedBytes, skipCandidates, yield) {
				return false
			}
			continue
		}
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn == nil || fn.Name == "" || len(fn.Name) > primitiveSourceByteBudget {
			continue
		}
		qualifiedBytes := len(namespace) + len(fn.Name)
		if namespace != "" {
			qualifiedBytes++
		}
		if qualifiedBytes > primitiveSourceByteBudget {
			continue
		}
		if skipCandidates != nil && *skipCandidates > 0 {
			*skipCandidates = *skipCandidates - 1
			continue
		}
		// The name is copied once for qualification and again into keyBytes;
		// include both copies, the origin, and fixed key metadata before doing
		// either allocation. The subtraction form is overflow-safe.
		candidateWork, ok := primitiveOwnerCandidatePreparationBytes(originBytes, qualifiedBytes)
		if !ok {
			return false
		}
		if preparedBytes != nil && !reservePrimitiveOwnerCandidateBytes(preparedBytes, candidateWork, primitiveOwnerCandidateWorkBudget) {
			return false
		}
		qualified := joinQualifiedName(namespace, fn.Name)
		if !yield(fn, qualified) {
			return false
		}
	}
	return true
}

func primitiveOwnerCandidatePreparationBytes(originBytes, qualifiedBytes int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	fixedBytes := 64 + primitiveOwnerCandidateNameBytes
	if originBytes < 0 || qualifiedBytes < 0 || originBytes > maxInt-fixedBytes {
		return 0, false
	}
	work := originBytes + fixedBytes
	if qualifiedBytes > (maxInt-work)/2 {
		return 0, false
	}
	return work + qualifiedBytes*2, true
}

func reservePrimitiveOwnerCandidateBytes(used *int, amount, budget int) bool {
	if amount < 0 || *used > budget || amount > budget-*used {
		return false
	}
	*used += amount
	return true
}

func preparePrimitiveOwnerCandidate(fn *ast.FuncDecl, qualifiedName, origin string, previous map[incremental.Key]bool) (identity [32]byte, name string, retained bool) {
	identity, name, retained, _ = preparePrimitiveOwnerCandidateWithBudget(fn, qualifiedName, origin, previous, nil)
	return identity, name, retained
}

func preparePrimitiveOwnerCandidateWithBudget(fn *ast.FuncDecl, qualifiedName, origin string, previous map[incremental.Key]bool, reserveLocalWork func(int) bool) (identity [32]byte, name string, retained bool, ready bool) {
	keyBytes := []byte("primitive/i64-parameters/v2\x00")
	keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(fn.Params)))
	keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(origin)))
	keyBytes = append(keyBytes, origin...)
	keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(qualifiedName)))
	keyBytes = append(keyBytes, qualifiedName...)
	identity = sha256.Sum256(keyBytes)
	name = primitiveOwnerCandidateName(identity)
	key := primitiveInput("function-source", name)
	retained = previous[key]
	if retained && len(fn.Body) > 1 {
		retained = false
		if len(fn.Body) <= 257 {
			if reserveLocalWork != nil {
				workBytes, ok := primitiveOwnerLocalClassificationBytes(fn)
				if !ok || !reserveLocalWork(workBytes) {
					return identity, name, false, false
				}
			}
			for _, stmt := range fn.Body {
				local, ok := stmt.(*ast.VarDeclStmt)
				if !ok || local == nil || len(local.Name) > primitiveSourceByteBudget {
					continue
				}
				id := sha256.Sum256([]byte(local.Name))
				if previous[primitiveInput("function-source", name+"/local-name-v2/"+hex.EncodeToString(id[:]))] {
					retained = true
					break
				}
			}
		}
	}
	return identity, name, retained, true
}

func primitiveOwnerLocalClassificationBytes(fn *ast.FuncDecl) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	workBytes := 0
	for _, stmt := range fn.Body {
		local, ok := stmt.(*ast.VarDeclStmt)
		if !ok || local == nil || len(local.Name) > primitiveSourceByteBudget {
			continue
		}
		amount := len(local.Name)
		if amount > maxInt-primitiveOwnerLocalNameWorkOverhead {
			return 0, false
		}
		amount += primitiveOwnerLocalNameWorkOverhead
		if amount > maxInt-workBytes {
			return 0, false
		}
		workBytes += amount
	}
	return workBytes, true
}

func primitiveOwnerCandidateName(identity [32]byte) string {
	return "primitive-v1:" + hex.EncodeToString(identity[:])
}

// primitiveOwnerCandidates lives only across this Analyze call's two admission
// passes, before semantic mutation. No AST pointers or records enter Session.
// Cold worlds stream as before; over-cap worlds keep the bounded prepared prefix
// and stream the unprepared suffix on each pass. previous remains the authority.
func primitiveOwnerCandidates(decls []ast.Decl, origin string, previous map[incremental.Key]bool, limit int, keepQualifiedNames bool) iter.Seq[primitiveOwnerCandidate] {
	stream := func(yield func(primitiveOwnerCandidate) bool) {
		walkPrimitiveOwnerDeclarations(decls, "", 0, len(origin), nil, nil, func(fn *ast.FuncDecl, qualified string) bool {
			identity, name, retained := preparePrimitiveOwnerCandidate(fn, qualified, origin, previous)
			if !keepQualifiedNames {
				qualified = ""
			}
			return yield(primitiveOwnerCandidate{function: fn, qualifiedName: qualified, name: name, identity: identity, retained: retained})
		})
	}
	if len(previous) == 0 || limit <= 0 || keepQualifiedNames {
		return stream
	}
	var candidates []primitiveOwnerCandidateRecord
	preparedBytes := 0
	complete := walkPrimitiveOwnerDeclarations(decls, "", 0, len(origin), &preparedBytes, nil, func(fn *ast.FuncDecl, qualified string) bool {
		if len(candidates) >= limit {
			return false
		}
		identity, _, retained, ready := preparePrimitiveOwnerCandidateWithBudget(fn, qualified, origin, previous, func(amount int) bool {
			return reservePrimitiveOwnerCandidateBytes(&preparedBytes, amount, primitiveOwnerCandidateWorkBudget)
		})
		if !ready {
			return false
		}
		candidates = append(candidates, primitiveOwnerCandidateRecord{function: fn, identity: identity, retained: retained})
		return true
	})
	if !complete {
		prefixLength := len(candidates)
		return func(yield func(primitiveOwnerCandidate) bool) {
			for _, record := range candidates {
				candidate := primitiveOwnerCandidate{function: record.function, identity: record.identity, retained: record.retained}
				if !yield(candidate) {
					return
				}
			}
			skip := prefixLength
			walkPrimitiveOwnerDeclarations(decls, "", 0, len(origin), nil, &skip, func(fn *ast.FuncDecl, qualified string) bool {
				identity, name, retained := preparePrimitiveOwnerCandidate(fn, qualified, origin, previous)
				if !keepQualifiedNames {
					qualified = ""
				}
				return yield(primitiveOwnerCandidate{function: fn, qualifiedName: qualified, name: name, identity: identity, retained: retained})
			})
		}
	}
	return func(yield func(primitiveOwnerCandidate) bool) {
		for _, record := range candidates {
			candidate := primitiveOwnerCandidate{function: record.function, identity: record.identity, retained: record.retained}
			if !yield(candidate) {
				return
			}
		}
	}
}
