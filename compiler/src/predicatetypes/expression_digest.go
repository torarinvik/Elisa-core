package predicatetypes

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"encoding/binary"
	"encoding/hex"
)

// ExpressionDigest preserves the canonical replay-key byte protocol.
func ExpressionDigest(fn *ast.FuncDecl, nodes []ast.Expr) string {
	data := make([]byte, 0, len(nodes)*12+len(fn.Name)+32)
	data = append(data, "canonical-scalar-typing-v3\x00"...)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Name)))
	data = append(data, fn.Name...)
	returnName := BuiltinSyntaxName(fn.ReturnType)
	data = binary.LittleEndian.AppendUint64(data, uint64(len(returnName)))
	data = append(data, returnName...)

	data = binary.LittleEndian.AppendUint64(data, uint64(len(fn.Params)))
	for _, param := range fn.Params {
		data = binary.LittleEndian.AppendUint64(data, uint64(len(param.Name)))
		data = append(data, param.Name...)
		typeName := BuiltinSyntaxName(param.Type)
		data = binary.LittleEndian.AppendUint64(data, uint64(len(typeName)))
		data = append(data, typeName...)

	}
	for _, expr := range nodes {
		switch node := expr.(type) {
		case *ast.Ident:
			data = append(data, 'N')
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Name)))
			data = append(data, node.Name...)
		case *ast.IntLit:
			data = append(data, 'I')
			if node.IsHex {
				data = append(data, 1)
			} else {
				data = append(data, 0)
			}
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Value)))
			data = append(data, node.Value...)
		case *ast.BoolLit:
			data = append(data, 'T')
			if node.Value {
				data = append(data, 1)
			} else {
				data = append(data, 0)
			}
		case *ast.CharLit:
			data = append(data, 'C')
			data = binary.LittleEndian.AppendUint64(data, uint64(len(node.Value)))
			data = append(data, node.Value...)
		case *ast.MembershipRangeExpr:
			data = append(data, 'R')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		case *ast.ParenExpr:
			data = append(data, 'P')
		case *ast.UnaryExpr:
			data = append(data, 'U')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		case *ast.BinaryExpr:
			data = append(data, 'B')
			data = binary.LittleEndian.AppendUint64(data, uint64(node.Op))
		}
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
