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
	if kind != "bool" {
		return fmt.Errorf("condition %q must return a boolean", source)
	}
	return nil
}

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
	} else if kind != "bool" {
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

func check(expr ast.Expr) (string, error) {
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return check(node.X)
	case *ast.BasicLit:
		if node.Kind == token.STRING {
			_, err := strconv.Unquote(node.Value)
			return "string", err
		}
	case *ast.Ident:
		switch node.Name {
		case "true", "false":
			return "bool", nil
		case "os", "arch", "mode":
			return "string", nil
		}
		return "", fmt.Errorf("unknown condition name %q", node.Name)
	case *ast.UnaryExpr:
		kind, err := check(node.X)
		if err != nil {
			return "", err
		}
		if node.Op == token.NOT && kind == "bool" {
			return "bool", nil
		}
	case *ast.BinaryExpr:
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
			return "bool", nil
		case token.LAND, token.LOR:
			if left == "bool" {
				return "bool", nil
			}
		}
	case *ast.CallExpr:
		name, ok := node.Fun.(*ast.Ident)
		if ok && name.Name == "wsl_tool" && len(node.Args) == 2 {
			first, err := check(node.Args[0])
			if err != nil {
				return "", err
			}
			second, err := check(node.Args[1])
			if err != nil {
				return "", err
			}
			if first != "string" || second != "string" {
				return "", fmt.Errorf("wsl_tool requires two strings")
			}
			return "bool", nil
		}
		if !ok || len(node.Args) != 1 {
			return "", fmt.Errorf("condition functions require one string argument")
		}
		kind, err := check(node.Args[0])
		if err != nil {
			return "", err
		}
		if kind != "string" {
			return "", fmt.Errorf("condition function argument must be a string")
		}
		switch name.Name {
		case "tool", "wsl", "package", "path":
			return "bool", nil
		case "env":
			return "string", nil
		default:
			return "", fmt.Errorf("unknown condition function %q", name.Name)
		}
	}
	return "", fmt.Errorf("unsupported condition expression %T", expr)
}

func eval(expr ast.Expr, facts Facts) (any, error) {
	switch node := expr.(type) {
	case *ast.ParenExpr:
		return eval(node.X, facts)
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return nil, fmt.Errorf("conditions support only quoted strings")
		}
		return strconv.Unquote(node.Value)
	case *ast.Ident:
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
	case *ast.UnaryExpr:
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
	case *ast.BinaryExpr:
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
	case *ast.CallExpr:
		name, ok := node.Fun.(*ast.Ident)
		if ok && name.Name == "wsl_tool" && len(node.Args) == 2 {
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
	return nil, fmt.Errorf("unsupported condition expression %T", expr)
}
