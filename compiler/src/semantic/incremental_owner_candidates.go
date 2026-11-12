package semantic

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"elisacore/src/ast"
	"elisacore/src/incremental"
	"elisacore/src/ownerinventory"
)

const primitiveOwnerCandidateLimit = 16384
const primitiveOwnerPointerCheckNodeLimit = 65536

// Cap total owner-candidate preparation work for one Analyze call. The charge
// covers declaration qualification, parameter validation/signature work,
// owner-key construction, retained-local classification, and candidate storage.
const primitiveOwnerCandidateWorkBudget = 8 * 1024 * 1024

// v2 owner names intentionally cannot match the former arity-only v1 keys.
const primitiveOwnerCandidateNamePrefix = "primitive-v2:"
const primitiveOwnerCandidateNameBytes = len(primitiveOwnerCandidateNamePrefix) + sha256.Size*2
const primitiveOwnerLocalNameWorkOverhead = 256

type primitiveOwnerCandidate struct {
	function      *ast.FuncDecl
	qualifiedName string // populated when lexical-read auditing needs source context
	name          string // precomputed owner-key segment used by both admission passes
	identity      [32]byte
	identityValid bool
	retained      bool
	auditLexical  bool
}

// primitiveOwnerCandidateRecord is the compact record used by the legacy
// prefix-iterator comparison helper. It retains no qualified-name or key strings.
type primitiveOwnerCandidateRecord struct {
	function      *ast.FuncDecl
	identity      [32]byte
	identityValid bool
	retained      bool
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
	return ownerinventory.ScanDeclarations(decls, depth, inventory.visitNode, inventory.add)
}

// walkPrimitiveOwnerDeclarations preserves declaration occurrence order,
// including aliased AST pointers in distinct namespaces. Analyze's admission
// layer must validate pointer uniqueness before using candidates.
func walkPrimitiveOwnerDeclarations(decls []ast.Decl, namespace string, depth int, originBytes int, preparedBytes *int, skipCandidates *int, yield func(*ast.FuncDecl, string) bool) bool {
	return ownerinventory.WalkCandidates(decls, namespace, depth, originBytes, preparedBytes, skipCandidates, primitiveSourceByteBudget, primitiveOwnerCandidateWorkBudget, primitiveOwnerCandidatePreparationBytes, reservePrimitiveOwnerCandidateBytes, yield)
}

func primitiveOwnerCandidatePreparationBytes(originBytes, qualifiedBytes int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	fixedBytes := 128 + primitiveOwnerCandidateNameBytes
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
	identity, name, retained, _, _ = preparePrimitiveOwnerCandidateWithBudget(fn, qualifiedName, origin, previous, nil)
	return identity, name, retained
}

func preparePrimitiveOwnerCandidateWithBudget(fn *ast.FuncDecl, qualifiedName, origin string, previous map[incremental.Key]bool, reserveWork func(int) bool) (identity [32]byte, name string, retained, identityValid, ready bool) {
	parameterTags, supported, ready := primitiveOwnerParameterTags(fn, reserveWork)
	if !ready {
		return identity, "", false, false, false
	}
	if !supported {
		return identity, "", false, false, true
	}
	keyBytes := []byte("primitive/parameter-signature/v3\x00")
	keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(fn.Params)))
	keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(origin)))
	keyBytes = append(keyBytes, origin...)
	keyBytes = binary.LittleEndian.AppendUint64(keyBytes, uint64(len(qualifiedName)))
	keyBytes = append(keyBytes, qualifiedName...)
	keyBytes = append(keyBytes, parameterTags[:len(fn.Params)]...)
	identity = sha256.Sum256(keyBytes)
	name = primitiveOwnerCandidateName(identity)
	identityValid = true
	key := primitiveInput("function-source", name)
	retained = previous[key]
	if retained && len(fn.Body) > 1 {
		retained = false
		if len(fn.Body) <= 257 {
			if reserveWork != nil && !reserveWork(len(fn.Body)*8) {
				return identity, name, false, identityValid, false
			}
			for _, stmt := range fn.Body {
				local, ok := stmt.(*ast.VarDeclStmt)
				if !ok || local == nil || len(local.Name) > primitiveSourceByteBudget {
					continue
				}
				workBytes := len(local.Name) + primitiveOwnerLocalNameWorkOverhead
				if reserveWork != nil && !reserveWork(workBytes) {
					return identity, name, false, identityValid, false
				}
				id := sha256.Sum256([]byte(local.Name))
				if previous[primitiveInput("function-source", name+"/local-name-v2/"+hex.EncodeToString(id[:]))] {
					retained = true
					break
				}
			}
		}
	}
	return identity, name, retained, identityValid, true
}

func primitiveOwnerParameterTags(fn *ast.FuncDecl, reserveWork func(int) bool) (tags [primitiveParameterLimit]byte, supported, ready bool) {
	return ownerinventory.ParameterTags(fn, primitiveParameterSchemaValid, primitiveSourceByteBudget, predicateBuiltinSyntaxName, reserveWork)
}

func primitiveOwnerCandidateName(identity [32]byte) string {
	return primitiveOwnerCandidateNamePrefix + hex.EncodeToString(identity[:])
}

// preparePrimitiveOwnerCandidates performs the warm-session owner walk before
// Analyze mutates session state. It materializes an occurrence-ordered list
// for retained-first and new-owner admission. The record cap counts only
// identities that can become owner keys; a separate lexical prefix retains
// context for the first bounded audit-function set. ready is false when a
// record or work cap stops the walk.
func preparePrimitiveOwnerCandidates(decls []ast.Decl, origin string, previous map[incremental.Key]bool, limit int, keepQualifiedNames bool) ([]primitiveOwnerCandidate, bool) {
	if limit <= 0 {
		return nil, false
	}
	workBytes := 0
	candidates := make([]primitiveOwnerCandidate, 0)
	cacheableRecords := 0
	lexicalOccurrences := 0
	complete := walkPrimitiveOwnerDeclarations(decls, "", 0, len(origin), &workBytes, nil, func(fn *ast.FuncDecl, qualified string) bool {
		auditLexical := keepQualifiedNames && lexicalOccurrences < lexicalReadMaxFunctions
		if keepQualifiedNames {
			lexicalOccurrences++
		}
		identity, name, retained, identityValid, ready := preparePrimitiveOwnerCandidateWithBudget(fn, qualified, origin, previous, func(amount int) bool {
			return reservePrimitiveOwnerCandidateBytes(&workBytes, amount, primitiveOwnerCandidateWorkBudget)
		})
		if !ready {
			return false
		}
		if identityValid {
			if cacheableRecords >= limit {
				return false
			}
			cacheableRecords++
		} else if !auditLexical {
			return true
		}
		if !auditLexical {
			qualified = ""
		}
		candidates = append(candidates, primitiveOwnerCandidate{
			function: fn, qualifiedName: qualified, name: name,
			identity: identity, identityValid: identityValid, retained: retained, auditLexical: auditLexical,
		})
		return true
	})
	if !complete {
		return candidates, false
	}
	return candidates, true
}
