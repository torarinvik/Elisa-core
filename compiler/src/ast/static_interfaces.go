package ast

import "elisacore/src/lexer"

type InterfaceDecl struct {
	Position lexer.Pos
	Name     string
	Members  []InterfaceMember
}

type InterfaceMember interface {
	Node
	interfaceMemberTag()
}

type AssociatedTypeDecl struct {
	Position lexer.Pos
	Name     string
}

type ImplDecl struct {
	Position      lexer.Pos
	Annotations   []Annotation
	InterfaceName string
	// GenericParams are the impl's own type parameters, from `impl[T] Iface for Box[T]:`.
	// They scope ForType and the impl members, making the impl a parametric (blanket) impl
	// that matches every instantiation of ForType's head constructor.
	GenericParams []GenericParam
	ForType       TypeExpr
	Members       []ImplMember
}

type ImplMember interface {
	Node
	implMemberTag()
}

type ImplAssociatedTypeDecl struct {
	Position lexer.Pos
	Name     string
	Type     TypeExpr
}

func (n *ImplDecl) IsExtension() bool {
	return n != nil && n.InterfaceName == ""
}

func (n *InterfaceDecl) Pos() lexer.Pos          { return n.Position }
func (n *AssociatedTypeDecl) Pos() lexer.Pos     { return n.Position }
func (n *ImplDecl) Pos() lexer.Pos               { return n.Position }
func (n *ImplAssociatedTypeDecl) Pos() lexer.Pos { return n.Position }

func (*InterfaceDecl) nodeTag()          {}
func (*AssociatedTypeDecl) nodeTag()     {}
func (*ImplDecl) nodeTag()               {}
func (*ImplAssociatedTypeDecl) nodeTag() {}

func (*InterfaceDecl) declTag() {}
func (*ImplDecl) declTag()      {}

func (*AssociatedTypeDecl) interfaceMemberTag() {}
func (*ExternFuncDecl) interfaceMemberTag()     {}

func (*ImplAssociatedTypeDecl) implMemberTag() {}
func (*FuncDecl) implMemberTag()               {}
func (*ExternFuncDecl) implMemberTag()         {}
