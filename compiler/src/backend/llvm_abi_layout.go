//go:build cgo

package backend

import (
	"fmt"
	"strconv"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func (g *llvmGenerator) checkAbiLayoutAnnotations(decl *ast.StructDecl, st *semantic.StructType) error {
	if decl == nil || st == nil {
		return nil
	}
	for _, annotation := range decl.Annotations {
		if annotation.Name != "abi_layout" {
			continue
		}
		if err := g.checkAbiLayoutAnnotation(decl, st, annotation); err != nil {
			return err
		}
	}
	return nil
}

func (g *llvmGenerator) checkAbiLayoutAnnotation(decl *ast.StructDecl, st *semantic.StructType, annotation ast.Annotation) error {
	for i := 0; i < len(annotation.Args); {
		key := strings.TrimSpace(annotation.Args[i])
		switch key {
		case "size":
			expected, err := parseBackendAbiLayoutUint(annotation.Args, i+1, decl.Name, key)
			if err != nil {
				return err
			}
			actual, err := g.abiSizeOfType(st)
			if err != nil {
				return err
			}
			if actual != expected {
				return fmt.Errorf("@abi_layout on struct %q expected size %d bytes, got %d", decl.Name, expected, actual)
			}
			i += 2
		case "align":
			expected, err := parseBackendAbiLayoutUint(annotation.Args, i+1, decl.Name, key)
			if err != nil {
				return err
			}
			actual, err := g.abiAlignmentOfType(st)
			if err != nil {
				return err
			}
			if actual != expected {
				return fmt.Errorf("@abi_layout on struct %q expected alignment %d bytes, got %d", decl.Name, expected, actual)
			}
			i += 2
		case "field":
			if i+2 >= len(annotation.Args) {
				return fmt.Errorf("@abi_layout on struct %q expects field, name, offset triples", decl.Name)
			}
			fieldName := strings.TrimSpace(annotation.Args[i+1])
			expected, err := parseBackendAbiLayoutUint(annotation.Args, i+2, decl.Name, "field "+fieldName)
			if err != nil {
				return err
			}
			actual, err := g.abiFieldOffset(st, fieldName)
			if err != nil {
				return err
			}
			if actual != expected {
				return fmt.Errorf("@abi_layout on struct %q expected field %q at offset %d, got %d", decl.Name, fieldName, expected, actual)
			}
			i += 3
		default:
			return fmt.Errorf("@abi_layout on struct %q uses unknown assertion key %q", decl.Name, key)
		}
	}
	return nil
}

func parseBackendAbiLayoutUint(args []string, index int, structName string, key string) (uint64, error) {
	if index >= len(args) {
		return 0, fmt.Errorf("@abi_layout on struct %q expects an integer byte value after %q", structName, key)
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(args[index]), 0, 64)
	if err != nil {
		return 0, fmt.Errorf("@abi_layout on struct %q expects a non-negative integer byte value after %q, got %q", structName, key, args[index])
	}
	return parsed, nil
}

func (g *llvmGenerator) abiFieldOffset(st *semantic.StructType, fieldName string) (uint64, error) {
	_, fieldIndex, containerType, _, err := g.fieldInfo(st, fieldName)
	if err != nil {
		return 0, err
	}
	containerLLVMType, err := g.lowerType(containerType)
	if err != nil {
		return 0, err
	}
	return g.abiOffsetOfLLVMElement(containerLLVMType, fieldIndex)
}
