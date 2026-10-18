package semantic

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"encoding/hex"
	"reflect"
)

// Pin every admitted syntax field, including default-zero fields. Review this
// manifest when changing syntax or the compact typing/source protocol.
var primitiveASTSchemas = map[reflect.Type]string{
	reflect.TypeOf(ast.Annotation{}):          "ada607abbf8a83bf1bd91c093c6785b4c60a2323a50e2cf1b134c6b165a6787f",
	reflect.TypeOf(ast.ListLitExpr{}):         "e210681a25bf0f37167bdc362fd29115a5e99c03ad29975967f1c6ceeab49668",
	reflect.TypeOf(ast.MembershipRangeExpr{}): "ba75fa2fec8ad47b0ec26fba14ffa739aba350f46adfe83652f7d81f8005d37e",
	reflect.TypeOf(ast.BinaryExpr{}):          "92e3d95dc9a0a6526b1f3234e63b451a8e9fa12ef790f47b0055f9bc2f885182",
	reflect.TypeOf(ast.BuiltinTypeExpr{}):     "360efd5d501b1304432413e9d3048338274255bb0a772e9f846edd9acb069534",
	reflect.TypeOf(ast.FuncDecl{}):            "fc82630065f2aa2dc6873a5f2112b548667de9a27a23e465d0478f1d664fdb0d",
	reflect.TypeOf(ast.IntLit{}):              "5af91f614d7c1fc3674f66ca44a1688af8ae996e4ad244ee4b16be334c4c84cb",
	reflect.TypeOf(ast.NamedType{}):           "351833f4cb4890074cea83f6ba5d1c1e9d1ccf63b23ff44769390a3c61562851",
	reflect.TypeOf(ast.ParenExpr{}):           "9bc0fd32d6e1dc94dfd4619ced30c94730fc4cbfa9d312891d8f37a0cd9cb1d0",
	reflect.TypeOf(ast.ReturnStmt{}):          "9b3f226168294ea3a0f33f674170fcbe478d98be6611bfed53889926f0c95789",
	reflect.TypeOf(ast.UnaryExpr{}):           "68d3742cc540001e45f98cd582b94f690ea6906216e16d2c17a57fd6bc3d5c42",
}

func primitiveSchemaMatches(typ reflect.Type, expected string) bool {
	fields := ""
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fields += field.Name + ":" + field.Type.String() + "\x00"
	}
	digest := sha256.Sum256([]byte(fields))
	return hex.EncodeToString(digest[:]) == expected
}

var primitiveSchemaValid = func() bool {
	for typ, expected := range primitiveASTSchemas {
		if !primitiveSchemaMatches(typ, expected) {
			return false
		}
	}
	return true
}()

// Both Go schemas are process-static; avoid rebuilding the Result inventory on
// every warm edit or on a project with no reusable bodies.
var primitiveResultSchemaValid = func() bool {
	return validateFunctionProductFields(reflect.TypeOf(Result{}), functionProductFieldPolicies) == nil
}()
