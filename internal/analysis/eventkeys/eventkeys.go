// Package eventkeys checks event construction in Nibiru-owned Go code.
package eventkeys

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const (
	modulePath = "github.com/NibiruChain/nibiru/v2/"
	sdkPath    = modulePath + "lib/cosmos-sdk/types"
	abciPath   = modulePath + "bft/abci/types"
	bankPath   = modulePath + "lib/cosmos-sdk/x/bank/types"
)

var Analyzer = &analysis.Analyzer{
	Name: "eventkeys",
	Doc: `check unique attribute keys and use of event constructors in Nibiru-owned code

The analyzer checks production code for repeated constant keys in sdk.NewEvent
calls, raw event construction, and direct writes to event attributes.
Use -audit to also list arguments whose keys are not visible at the call site.`,
	Run: run,
}

var audit bool

func init() {
	Analyzer.Flags.BoolVar(&audit, "audit", false, "also report attribute keys the analyzer cannot resolve")
}

func run(pass *analysis.Pass) (any, error) {
	if !ownsEvents(pass.Pkg.Path()) {
		return nil, nil
	}
	for _, file := range pass.Files {
		if ast.IsGenerated(file) || strings.HasSuffix(pass.Fset.PositionFor(file.Pos(), false).Filename, "_test.go") {
			continue
		}
		ast.PreorderStack(file, nil, func(node ast.Node, stack []ast.Node) bool {
			switch node := node.(type) {
			case *ast.Ident:
				fn, _ := pass.TypesInfo.Uses[node].(*types.Func)
				if fn != nil && fn.Name() == "AppendAttributes" {
					if recv := fn.Signature().Recv(); recv != nil && isRawEvent(recv.Type()) {
						pass.Reportf(node.Pos(), "pass attributes to sdk.NewEvent instead of calling AppendAttributes")
					}
				}
			case *ast.CallExpr:
				checkCall(pass, node, stack)
			case *ast.CompositeLit:
				if isRawEvent(pass.TypesInfo.TypeOf(node)) && compatibilityUse(pass, stack) != rawLiteral {
					pass.Reportf(node.Pos(), "construct Nibiru-owned events with sdk.NewEvent")
				}
			case *ast.AssignStmt:
				for _, target := range node.Lhs {
					checkAttributeWrite(pass, target)
				}
			case *ast.IncDecStmt:
				checkAttributeWrite(pass, node.X)
			case *ast.RangeStmt:
				if node.Tok == token.ASSIGN {
					checkAttributeWrite(pass, node.Key)
					checkAttributeWrite(pass, node.Value)
				}
			case *ast.UnaryExpr:
				if node.Op == token.AND {
					checkAttributeWrite(pass, node.X)
				}
			}
			return true
		})
	}
	return nil, nil
}

func ownsEvents(path string) bool {
	path, ok := strings.CutPrefix(path, modulePath)
	if !ok {
		return false
	}
	return path == "app" || strings.HasPrefix(path, "app/") ||
		path == "evm" || strings.HasPrefix(path, "evm/") ||
		strings.HasPrefix(path, "x/") || path == "lib/cosmos-sdk/x/bank/types"
}

func isSDKFunction(fn *types.Func, name string) bool {
	return fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == sdkPath && fn.Name() == name &&
		fn.Signature().Recv() == nil
}

func isRawEvent(t types.Type) bool {
	named := namedType(t)
	if named == nil {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "Event" && obj.Pkg() != nil &&
		(obj.Pkg().Path() == sdkPath || obj.Pkg().Path() == abciPath)
}

func namedType(t types.Type) *types.Named {
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	named, _ := t.(*types.Named)
	return named
}

func function(info *types.Info, expr ast.Expr) *types.Func {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.Ident:
		fn, _ := info.Uses[expr].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := info.Uses[expr.Sel].(*types.Func)
		return fn
	default:
		return nil
	}
}

func checkCall(pass *analysis.Pass, call *ast.CallExpr, stack []ast.Node) {
	if ident, ok := ast.Unparen(call.Fun).(*ast.Ident); ok {
		if builtin, ok := pass.TypesInfo.Uses[ident].(*types.Builtin); ok {
			switch builtin.Name() {
			case "clear", "copy":
				checkAttributeWrite(pass, call.Args[0])
			}
		}
	}
	if pass.TypesInfo.Types[call.Fun].IsType() && isRawEvent(pass.TypesInfo.TypeOf(call)) {
		if compatibilityUse(pass, stack) != rawConversion {
			pass.Reportf(call.Pos(), "raw event conversions are reserved for compatibility boundaries")
		}
		return
	}

	if !isSDKFunction(function(pass.TypesInfo, call.Fun), "NewEvent") {
		return
	}
	attrs := call.Args[1:]
	if call.Ellipsis.IsValid() {
		literal, ok := ast.Unparen(attrs[0]).(*ast.CompositeLit)
		if !ok {
			reportUnresolved(pass, attrs[0].Pos())
			return
		}
		attrs = literal.Elts
	}

	seen := make(map[string]struct{}, len(attrs))
	for _, expr := range attrs {
		// A slice literal can have keyed element indices.
		if elt, ok := expr.(*ast.KeyValueExpr); ok {
			expr = elt.Value
		}
		key, pos, known := attributeKey(pass.TypesInfo, expr)
		if !known {
			reportUnresolved(pass, pos)
			continue
		}
		if _, ok := seen[key]; ok {
			pass.Reportf(pos, "duplicate event attribute key %q", key)
		}
		seen[key] = struct{}{}
	}
}

func attributeKey(info *types.Info, expr ast.Expr) (string, token.Pos, bool) {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.CallExpr:
		if isSDKFunction(function(info, expr.Fun), "NewAttribute") {
			return constantKey(info, expr.Args[0])
		}
	case *ast.CompositeLit:
		for i, elt := range expr.Elts {
			if pair, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "Key" {
					return constantKey(info, pair.Value)
				}
			} else if i == 0 {
				return constantKey(info, elt)
			}
		}
		// The SDK attribute's Key field has the zero value when omitted.
		return "", expr.Pos(), true
	}
	return "", expr.Pos(), false
}

func constantKey(info *types.Info, expr ast.Expr) (string, token.Pos, bool) {
	value := info.Types[expr].Value
	if value == nil || value.Kind() != constant.String {
		return "", expr.Pos(), false
	}
	return constant.StringVal(value), expr.Pos(), true
}

func reportUnresolved(pass *analysis.Pass, pos token.Pos) {
	if audit {
		pass.Reportf(pos, "cannot resolve event attribute key statically")
	}
}

func checkAttributeWrite(pass *analysis.Pass, target ast.Expr) {
	for target != nil {
		switch expr := ast.Unparen(target).(type) {
		case *ast.SelectorExpr:
			if selectsRawAttributes(pass.TypesInfo, expr) {
				pass.Reportf(expr.Pos(), "set attributes during event construction instead of mutating raw event attributes")
				return
			}
			target = expr.X
		case *ast.CallExpr:
			fn := function(pass.TypesInfo, expr.Fun)
			if fn != nil && fn.Name() == "GetAttributes" {
				if recv := fn.Signature().Recv(); recv != nil && isRawEvent(recv.Type()) {
					pass.Reportf(expr.Pos(), "set attributes during event construction instead of mutating raw event attributes")
				}
			}
			return
		case *ast.IndexExpr:
			target = expr.X
		case *ast.SliceExpr:
			target = expr.X
		case *ast.StarExpr:
			target = expr.X
		default:
			return
		}
	}
}

func selectsRawAttributes(info *types.Info, expr *ast.SelectorExpr) bool {
	selection := info.Selections[expr]
	if expr.Sel.Name != "Attributes" || selection == nil || selection.Kind() != types.FieldVal {
		return false
	}
	owner := selection.Recv()
	path := selection.Index()
	// Follow embedded fields to the type that declares Attributes.
	for _, index := range path[:len(path)-1] {
		if ptr, ok := owner.Underlying().(*types.Pointer); ok {
			owner = ptr.Elem()
		}
		owner = owner.Underlying().(*types.Struct).Field(index).Type()
	}
	return isRawEvent(owner)
}

type rawUse int

const (
	noRawUse rawUse = iota
	rawConversion
	rawLiteral
)

// Each compatibility path allows only the operation needed to carry existing
// attributes across a protocol or decoding boundary.
func compatibilityUse(pass *analysis.Pass, stack []ast.Node) rawUse {
	for i := len(stack) - 1; i >= 0; i-- {
		decl, ok := stack[i].(*ast.FuncDecl)
		if !ok {
			continue
		}
		fn := pass.TypesInfo.Defs[decl.Name].(*types.Func)
		switch pass.Pkg.Path() {
		case modulePath + "x/wasm/keeper":
			if isDispatchMsg(fn) {
				return rawConversion
			}
		case modulePath + "app/wasmext":
			if isDispatchMsg(fn) {
				return rawConversion
			}
		case modulePath + "x/nutil/testutil":
			if fn.Name() == "RequireContainsTypedEvent" && fn.Signature().Recv() == nil {
				return rawLiteral
			}
		}
		return noRawUse
	}
	return noRawUse
}

func isDispatchMsg(fn *types.Func) bool {
	recv := fn.Signature().Recv()
	if recv == nil || fn.Name() != "DispatchMsg" {
		return false
	}
	named := namedType(recv.Type())
	return named != nil && named.Obj().Name() == "SDKMessageHandler"
}
