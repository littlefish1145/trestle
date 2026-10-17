// Package condition parses and evaluates the sandboxed boolean expressions used
// to enable targets and settings. Only the supported read-only facts and helper
// functions are available; Go functions, file access, and shell commands are not.
package condition

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// Facts supplies only the explicitly supported read-only queries. Conditions
// cannot call Go functions, access files directly, or run shell commands.
type Facts struct {
	OS, Arch, Mode string
	Tool           func(string) bool
	WSL            func(string) bool
	WSLTool        func(string, string) bool
	Package        func(string) bool
	Env            func(string) string
	Path           func(string) bool
}

// The two value kinds a condition expression may produce, used by the static
// type checker to reject expressions before they are evaluated.
const (
	kindBool   = "bool"
	kindString = "string"
)

// Validate reports whether source is a well-formed condition that evaluates to a
// boolean. An empty source is valid and means always enabled.
func Validate(source string) error {
	if source == "" {
		return nil
	}
	expr, err := parser.ParseExpr(source)
	if err != nil {
		return fmt.Errorf("invalid condition %q: %w", source, err)
	}
	kind, err := check(expr)
	if err != nil {
		return err
	}
	if kind != kindBool {
		return fmt.Errorf("condition %q must return a boolean", source)
	}
	return nil
}

// Evaluate parses source and resolves it against facts, returning the resulting
// boolean. An empty source evaluates to true.
func Evaluate(source string, facts Facts) (bool, error) {
	if source == "" {
		return true, nil
	}
	expr, err := parser.ParseExpr(source)
	if err != nil {
		return false, fmt.Errorf("invalid condition %q: %w", source, err)
	}
	if kind, err := check(expr); err != nil {
		return false, err
	} else if kind != kindBool {
		return false, fmt.Errorf("condition %q must return a boolean", source)
	}
	value, err := eval(expr, facts)
	if err != nil {
		return false, err
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("condition %q must return a boolean", source)
	}
	return result, nil
}

// check statically verifies that expr is supported and returns the kind of value
// it produces ("bool" or "string"). It never evaluates the expression.
func check(expr ast.Expr) (string, error) {
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return check(node.X)
	case *ast.BasicLit:
		return checkLiteral(node)
	case *ast.Ident:
		return checkIdent(node)
	case *ast.UnaryExpr:
		return checkUnary(node)
	case *ast.BinaryExpr:
		return checkBinary(node)
	case *ast.CallExpr:
		return checkCall(node)
	}
	return "", fmt.Errorf("unsupported condition expression %T", expr)
}

// checkLiteral accepts a quoted string literal and rejects every other literal.
func checkLiteral(node *ast.BasicLit) (string, error) {
	if node.Kind != token.STRING {
		return "", fmt.Errorf("unsupported condition expression %T", node)
	}
	_, err := strconv.Unquote(node.Value)
	return kindString, err
}

// checkIdent resolves the built-in boolean and string identifiers.
func checkIdent(node *ast.Ident) (string, error) {
	switch node.Name {
	case "true", "false":
		return kindBool, nil
	case "os", "arch", "mode":
		return kindString, nil
	}
	return "", fmt.Errorf("unknown condition name %q", node.Name)
}

// checkUnary accepts only the boolean negation operator.
func checkUnary(node *ast.UnaryExpr) (string, error) {
	kind, err := check(node.X)
	if err != nil {
		return "", err
	}
	if node.Op == token.NOT && kind == kindBool {
		return kindBool, nil
	}
	return "", fmt.Errorf("unsupported condition expression %T", node)
}

// checkBinary requires both operands to share a kind and restricts the operator
// set to comparisons and boolean logic.
func checkBinary(node *ast.BinaryExpr) (string, error) {
	left, err := check(node.X)
	if err != nil {
		return "", err
	}
	right, err := check(node.Y)
	if err != nil {
		return "", err
	}
	if left != right {
		return "", fmt.Errorf("condition compares different types")
	}
	switch node.Op {
	case token.EQL, token.NEQ:
		return kindBool, nil
	case token.LAND, token.LOR:
		if left == kindBool {
			return kindBool, nil
		}
	}
	return "", fmt.Errorf("unsupported condition expression %T", node)
}

// checkCall validates the wsl_tool two-argument form and the single-string
// helper functions, rejecting anything else.
func checkCall(node *ast.CallExpr) (string, error) {
	name, ok := node.Fun.(*ast.Ident)
	if ok && name.Name == "wsl_tool" && len(node.Args) == 2 {
		return checkWSLToolCall(node)
	}
	if !ok || len(node.Args) != 1 {
		return "", fmt.Errorf("condition functions require one string argument")
	}
	kind, err := check(node.Args[0])
	if err != nil {
		return "", err
	}
	if kind != kindString {
		return "", fmt.Errorf("condition function argument must be a string")
	}
	switch name.Name {
	case "tool", "wsl", "package", "path":
		return kindBool, nil
	case "env":
		return kindString, nil
	default:
		return "", fmt.Errorf("unknown condition function %q", name.Name)
	}
}

// checkWSLToolCall validates the wsl_tool(distribution, executable) call, whose
// two arguments must both be strings.
func checkWSLToolCall(node *ast.CallExpr) (string, error) {
	first, err := check(node.Args[0])
	if err != nil {
		return "", err
	}
	second, err := check(node.Args[1])
	if err != nil {
		return "", err
	}
	if first != kindString || second != kindString {
		return "", fmt.Errorf("wsl_tool requires two strings")
	}
	return kindBool, nil
}

// eval resolves a validated expression against the supplied facts.
func eval(expr ast.Expr, facts Facts) (any, error) {
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return eval(node.X, facts)
	case *ast.BasicLit:
		return evalLiteral(node)
	case *ast.Ident:
		return evalIdent(node, facts)
	case *ast.UnaryExpr:
		return evalUnary(node, facts)
	case *ast.BinaryExpr:
		return evalBinary(node, facts)
	case *ast.CallExpr:
		return evalCall(node, facts)
	}
	return nil, fmt.Errorf("unsupported condition expression %T", expr)
}

// evalLiteral returns the unquoted value of a string literal.
func evalLiteral(node *ast.BasicLit) (any, error) {
	if node.Kind != token.STRING {
		return nil, fmt.Errorf("conditions support only quoted strings")
	}
	return strconv.Unquote(node.Value)
}

// evalIdent returns the value of a built-in identifier from the facts.
func evalIdent(node *ast.Ident, facts Facts) (any, error) {
	switch node.Name {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "os":
		return facts.OS, nil
	case "arch":
		return facts.Arch, nil
	case "mode":
		return facts.Mode, nil
	default:
		return nil, fmt.Errorf("unknown condition name %q", node.Name)
	}
}

// evalUnary applies the boolean negation operator.
func evalUnary(node *ast.UnaryExpr, facts Facts) (any, error) {
	if node.Op != token.NOT {
		return nil, fmt.Errorf("unsupported unary operator %s", node.Op)
	}
	value, err := eval(node.X, facts)
	if err != nil {
		return nil, err
	}
	flag, ok := value.(bool)
	if !ok {
		return nil, fmt.Errorf("! requires a boolean")
	}
	return !flag, nil
}

// evalBinary applies a comparison or a short-circuiting logical operator.
func evalBinary(node *ast.BinaryExpr, facts Facts) (any, error) {
	left, err := eval(node.X, facts)
	if err != nil {
		return nil, err
	}
	if node.Op == token.LAND || node.Op == token.LOR {
		flag, ok := left.(bool)
		if !ok {
			return nil, fmt.Errorf("logical operators require booleans")
		}
		if node.Op == token.LAND && !flag {
			return false, nil
		}
		if node.Op == token.LOR && flag {
			return true, nil
		}
	}
	right, err := eval(node.Y, facts)
	if err != nil {
		return nil, err
	}
	switch node.Op {
	case token.LAND, token.LOR:
		flag, ok := right.(bool)
		if !ok {
			return nil, fmt.Errorf("logical operators require booleans")
		}
		return flag, nil
	case token.EQL:
		return left == right, nil
	case token.NEQ:
		return left != right, nil
	default:
		return nil, fmt.Errorf("unsupported condition operator %s", node.Op)
	}
}

// evalCall evaluates the wsl_tool form and the single-string helper functions.
func evalCall(node *ast.CallExpr, facts Facts) (any, error) {
	name, ok := node.Fun.(*ast.Ident)
	if ok && name.Name == "wsl_tool" && len(node.Args) == 2 {
		return evalWSLToolCall(node, facts)
	}
	if !ok || len(node.Args) != 1 {
		return nil, fmt.Errorf("condition functions require one string argument")
	}
	argument, err := eval(node.Args[0], facts)
	if err != nil {
		return nil, err
	}
	value, ok := argument.(string)
	if !ok {
		return nil, fmt.Errorf("condition function argument must be a string")
	}
	switch name.Name {
	case "tool":
		if facts.Tool != nil {
			return facts.Tool(value), nil
		}
		return false, nil
	case "wsl":
		if facts.WSL != nil {
			return facts.WSL(value), nil
		}
		return false, nil
	case "package":
		if facts.Package != nil {
			return facts.Package(value), nil
		}
		return false, nil
	case "env":
		if facts.Env != nil {
			return facts.Env(value), nil
		}
		return "", nil
	case "path":
		if facts.Path != nil {
			return facts.Path(value), nil
		}
		return false, nil
	default:
		return nil, fmt.Errorf("unknown condition function %q", name.Name)
	}
}

// evalWSLToolCall evaluates wsl_tool(distribution, executable), defaulting to
// false when no probe is configured.
func evalWSLToolCall(node *ast.CallExpr, facts Facts) (any, error) {
	first, err := eval(node.Args[0], facts)
	if err != nil {
		return nil, err
	}
	second, err := eval(node.Args[1], facts)
	if err != nil {
		return nil, err
	}
	distribution, okFirst := first.(string)
	executable, okSecond := second.(string)
	if !okFirst || !okSecond {
		return nil, fmt.Errorf("wsl_tool requires two strings")
	}
	if facts.WSLTool != nil {
		return facts.WSLTool(distribution, executable), nil
	}
	return false, nil
}
