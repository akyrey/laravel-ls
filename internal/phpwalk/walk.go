package phpwalk

import (
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/akyrey/laravel-lsp/internal/phpnode"
)

// Walk traverses the PHP CST rooted at tree.RootNode(), calling v for each
// relevant node kind. path is the file path used to populate Location fields
// in Info structs. Traversal is depth-first, pre-order; the visitor cannot
// stop or prune recursion.
func Walk(path string, src []byte, tree *ts.Tree, v Visitor) {
	walkNode(path, src, tree.RootNode(), v)
}

// WalkNode is like Walk but starts at an arbitrary node. Useful for sub-walks
// (e.g. walking a class body with a different visitor).
func WalkNode(path string, src []byte, root *ts.Node, v Visitor) {
	walkNode(path, src, root, v)
}

func walkNode(path string, src []byte, n *ts.Node, v Visitor) {
	switch n.Kind() {
	case "namespace_definition":
		if nameNode := n.ChildByFieldName("name"); nameNode != nil {
			v.VisitNamespace(namespaceName(nameNode, src))
		}

	case "namespace_use_declaration":
		emitUseItems(src, n, v)
		return // children already processed inside emitUseItems

	case "class_declaration":
		v.VisitClass(buildClassInfo(path, src, n))

	case "interface_declaration":
		v.VisitInterface(buildInterfaceInfo(path, src, n))

	case "method_declaration":
		v.VisitClassMethod(buildMethodInfo(path, src, n))

	case "property_declaration":
		emitProperties(path, src, n, v)

	case "member_access_expression", "nullsafe_member_access_expression":
		if info, ok := buildPropertyFetchInfo(path, src, n); ok {
			v.VisitPropertyFetch(info)
		}

	case "class_constant_access_expression":
		if info, ok := buildClassConstFetchInfo(path, src, n); ok {
			v.VisitClassConstFetch(info)
		}

	case "object_creation_expression":
		if info, ok := buildNewExprInfo(path, src, n); ok {
			v.VisitNew(info)
		}

	case "scoped_call_expression":
		if info, ok := buildStaticCallInfo(path, src, n); ok {
			v.VisitStaticCall(info)
		}

	case "member_call_expression":
		if info, ok := buildMethodCallInfo(path, src, n); ok {
			v.VisitMethodCall(info)
		}

	case "binary_expression":
		// PHP's `instanceof` is a binary_expression with operator "instanceof".
		if opNode := n.ChildByFieldName("operator"); opNode != nil && opNode.Kind() == "instanceof" {
			if info, ok := buildInstanceOfInfo(path, src, n); ok {
				v.VisitInstanceOf(info)
			}
		}

	case "assignment_expression":
		v.VisitAssign(buildAssignInfo(src, n))
	}

	for i := uint(0); i < n.ChildCount(); i++ {
		walkNode(path, src, n.Child(i), v)
	}
}

// ── namespace helpers ──────────────────────────────────────────────────────

func namespaceName(n *ts.Node, src []byte) string {
	var parts []string
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child.Kind() == "name" {
			parts = append(parts, phpnode.NodeText(child, src))
		}
	}
	return strings.Join(parts, "\\")
}

func qualifiedNameText(n *ts.Node, src []byte) string {
	return phpnode.NodeText(n, src)
}

func emitUseItems(src []byte, n *ts.Node, v Visitor) {
	groupPrefix := ""
	var groupNode *ts.Node

	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		switch child.Kind() {
		case "namespace_use_group":
			groupNode = child
		case "namespace_name":
			if groupNode == nil {
				groupPrefix = namespaceName(child, src)
			}
		}
	}

	if groupNode != nil {
		for i := uint(0); i < groupNode.ChildCount(); i++ {
			clause := groupNode.Child(i)
			if clause.Kind() != "namespace_use_clause" {
				continue
			}
			name, alias := parseUseClause(src, clause, groupPrefix)
			if name != "" {
				v.VisitUseItem(alias, name)
			}
		}
		return
	}

	for i := uint(0); i < n.ChildCount(); i++ {
		clause := n.Child(i)
		if clause.Kind() != "namespace_use_clause" {
			continue
		}
		name, alias := parseUseClause(src, clause, "")
		if name != "" {
			v.VisitUseItem(alias, name)
		}
	}
}

func parseUseClause(src []byte, clause *ts.Node, prefix string) (fqn, alias string) {
	if aliasNode := clause.ChildByFieldName("alias"); aliasNode != nil {
		if aliasNode.Kind() == "name" {
			alias = phpnode.NodeText(aliasNode, src)
		} else {
			for i := uint(0); i < aliasNode.ChildCount(); i++ {
				if c := aliasNode.Child(i); c.Kind() == "name" {
					alias = phpnode.NodeText(c, src)
					break
				}
			}
		}
	}

	for i := uint(0); i < clause.ChildCount(); i++ {
		child := clause.Child(i)
		switch child.Kind() {
		case "qualified_name":
			raw := qualifiedNameText(child, src)
			if prefix != "" {
				fqn = prefix + "\\" + raw
			} else {
				fqn = raw
			}
		case "name":
			if fqn == "" {
				raw := phpnode.NodeText(child, src)
				if prefix != "" {
					fqn = prefix + "\\" + raw
				} else {
					fqn = raw
				}
			}
		}
	}

	if alias == "" && fqn != "" {
		parts := strings.Split(fqn, "\\")
		alias = parts[len(parts)-1]
	}
	return fqn, alias
}

// ── class / interface ──────────────────────────────────────────────────────

func buildClassInfo(path string, src []byte, n *ts.Node) ClassInfo {
	info := ClassInfo{
		Raw:      n,
		Src:      src,
		Location: phpnode.FromNode(path, n),
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		info.NameText = phpnode.NodeText(nameNode, src)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child.Kind() == "base_clause" {
			info.ExtendsText = baseClauseText(src, child)
			break
		}
	}
	return info
}

func baseClauseText(src []byte, clause *ts.Node) string {
	for i := uint(0); i < clause.ChildCount(); i++ {
		child := clause.Child(i)
		switch child.Kind() {
		case "qualified_name":
			return qualifiedNameText(child, src)
		case "name":
			return phpnode.NodeText(child, src)
		}
	}
	return ""
}

func buildInterfaceInfo(path string, src []byte, n *ts.Node) InterfaceInfo {
	info := InterfaceInfo{Raw: n, Src: src, Location: phpnode.FromNode(path, n)}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		info.NameText = phpnode.NodeText(nameNode, src)
	}
	return info
}

// ── method ────────────────────────────────────────────────────────────────

func buildMethodInfo(path string, src []byte, n *ts.Node) MethodInfo {
	info := MethodInfo{
		Raw:       n,
		Src:       src,
		Location:  phpnode.FromNode(path, n),
		StartByte: int(n.StartByte()),
		EndByte:   int(n.EndByte()),
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		info.Name = phpnode.NodeText(nameNode, src)
	}
	if rtNode := n.ChildByFieldName("return_type"); rtNode != nil {
		info.ReturnTypeText = unwrapTypeName(src, rtNode)
	}
	if paramsNode := n.ChildByFieldName("parameters"); paramsNode != nil {
		info.Params = extractParams(src, paramsNode)
	}
	return info
}

func unwrapTypeName(src []byte, n *ts.Node) string {
	switch n.Kind() {
	case "named_type":
		for i := uint(0); i < n.ChildCount(); i++ {
			child := n.Child(i)
			if child.Kind() == "name" || child.Kind() == "qualified_name" {
				return phpnode.NodeText(child, src)
			}
		}
	case "optional_type":
		for i := uint(0); i < n.ChildCount(); i++ {
			child := n.Child(i)
			if child.Kind() != "?" {
				return unwrapTypeName(src, child)
			}
		}
	case "primitive_type":
		return phpnode.NodeText(n, src)
	case "qualified_name":
		return qualifiedNameText(n, src)
	case "name":
		return phpnode.NodeText(n, src)
	}
	return phpnode.NodeText(n, src)
}

func extractParams(src []byte, paramsNode *ts.Node) []ParamInfo {
	var out []ParamInfo
	for i := uint(0); i < paramsNode.ChildCount(); i++ {
		child := paramsNode.Child(i)
		if child.Kind() != "simple_parameter" && child.Kind() != "variadic_parameter" {
			continue
		}
		p := ParamInfo{}
		if typeNode := child.ChildByFieldName("type"); typeNode != nil {
			p.TypeText = unwrapTypeName(src, typeNode)
		}
		if varNode := child.ChildByFieldName("name"); varNode != nil {
			p.VarName = phpnode.NodeText(varNode, src)
		}
		if p.VarName != "" {
			out = append(out, p)
		}
	}
	return out
}

// ── properties ────────────────────────────────────────────────────────────

func emitProperties(path string, src []byte, n *ts.Node, v Visitor) {
	loc := phpnode.FromNode(path, n)
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		if child.Kind() != "property_element" {
			continue
		}
		info := PropertyInfo{Location: loc, Src: src}
		if nameNode := child.ChildByFieldName("name"); nameNode != nil {
			raw := phpnode.NodeText(nameNode, src)
			info.PropName = strings.TrimPrefix(raw, "$")
		}
		if valNode := child.ChildByFieldName("default_value"); valNode != nil {
			info.ValueRaw = valNode
		}
		if info.PropName != "" {
			v.VisitProperty(info)
		}
	}
}

// ── expression helpers ────────────────────────────────────────────────────

func buildPropertyFetchInfo(path string, src []byte, n *ts.Node) (PropertyFetchInfo, bool) {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil || (nameNode.Kind() != "name" && nameNode.Kind() != "variable_name") {
		return PropertyFetchInfo{}, false
	}
	propText := phpnode.NodeText(nameNode, src)
	propText = strings.TrimPrefix(propText, "$")
	varNode := n.ChildByFieldName("object")
	if varNode == nil {
		return PropertyFetchInfo{}, false
	}
	return PropertyFetchInfo{
		PropName:     propText,
		PropLocation: phpnode.FromNode(path, nameNode),
		VarRaw:       varNode,
		Raw:          n,
		Src:          src,
	}, true
}

func buildClassConstFetchInfo(path string, src []byte, n *ts.Node) (ClassConstFetchInfo, bool) {
	var scopeNode, constNode *ts.Node
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		switch child.Kind() {
		case "name", "qualified_name", "variable_name":
			if scopeNode == nil {
				scopeNode = child
			} else {
				constNode = child
			}
		}
	}
	if scopeNode == nil || constNode == nil {
		return ClassConstFetchInfo{}, false
	}
	return ClassConstFetchInfo{
		ClassName:     phpnode.NodeText(scopeNode, src),
		ConstName:     phpnode.NodeText(constNode, src),
		ClassLocation: phpnode.FromNode(path, scopeNode),
		Raw:           n,
		Src:           src,
	}, true
}

func buildNewExprInfo(path string, src []byte, n *ts.Node) (NewExprInfo, bool) {
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		switch child.Kind() {
		case "qualified_name", "name":
			return NewExprInfo{
				ClassName:     phpnode.NodeText(child, src),
				ClassLocation: phpnode.FromNode(path, child),
				Raw:           n,
				Src:           src,
			}, true
		}
	}
	return NewExprInfo{}, false
}

func buildStaticCallInfo(path string, src []byte, n *ts.Node) (StaticCallInfo, bool) {
	scopeNode := n.ChildByFieldName("scope")
	nameNode := n.ChildByFieldName("name")
	if scopeNode == nil || nameNode == nil {
		return StaticCallInfo{}, false
	}
	info := StaticCallInfo{
		ClassName:     phpnode.NodeText(scopeNode, src),
		MethodName:    phpnode.NodeText(nameNode, src),
		ClassLocation: phpnode.FromNode(path, scopeNode),
		Location:      phpnode.FromNode(path, n),
		Raw:           n,
		Src:           src,
	}
	if argsNode := n.ChildByFieldName("arguments"); argsNode != nil {
		info.Args = extractArgExprs(src, argsNode)
	}
	return info, true
}

func buildMethodCallInfo(path string, src []byte, n *ts.Node) (MethodCallInfo, bool) {
	objNode := n.ChildByFieldName("object")
	nameNode := n.ChildByFieldName("name")
	if objNode == nil || nameNode == nil {
		return MethodCallInfo{}, false
	}
	info := MethodCallInfo{
		MethodName: phpnode.NodeText(nameNode, src),
		VarRaw:     objNode,
		Location:   phpnode.FromNode(path, n),
		Raw:        n,
		Src:        src,
	}
	if argsNode := n.ChildByFieldName("arguments"); argsNode != nil {
		info.Args = extractArgExprs(src, argsNode)
	}
	return info, true
}

func buildInstanceOfInfo(path string, src []byte, n *ts.Node) (InstanceOfInfo, bool) {
	// binary_expression with operator "instanceof":
	// left: expr  operator: instanceof  right: name|qualified_name
	rightNode := n.ChildByFieldName("right")
	if rightNode == nil {
		return InstanceOfInfo{}, false
	}
	return InstanceOfInfo{
		ClassName:     phpnode.NodeText(rightNode, src),
		ClassLocation: phpnode.FromNode(path, rightNode),
		Raw:           n,
		Src:           src,
	}, true
}

func buildAssignInfo(src []byte, n *ts.Node) AssignInfo {
	info := AssignInfo{Raw: n, Src: src}
	if leftNode := n.ChildByFieldName("left"); leftNode != nil {
		if leftNode.Kind() == "variable_name" {
			info.VarName = phpnode.NodeText(leftNode, src)
		}
	}
	if rightNode := n.ChildByFieldName("right"); rightNode != nil {
		info.RHSRaw = rightNode
	}
	return info
}

// ArgExprs unwraps the argument wrapper layer from an arguments node and
// returns the inner expression nodes.
func ArgExprs(argsNode *ts.Node, src []byte) []*ts.Node {
	return extractArgExprs(src, argsNode)
}

func extractArgExprs(src []byte, argsNode *ts.Node) []*ts.Node {
	var out []*ts.Node
	for i := uint(0); i < argsNode.ChildCount(); i++ {
		child := argsNode.Child(i)
		if child.Kind() != "argument" {
			continue
		}
		if val := child.ChildByFieldName("value"); val != nil {
			out = append(out, val)
		} else if child.NamedChildCount() > 0 {
			out = append(out, child.NamedChild(0))
		}
	}
	return out
}
