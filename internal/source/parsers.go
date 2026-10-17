package source

import (
	"os"
	"path/filepath"
	"strings"
)

// RustParser handles .rs files, extracting use imports and #[test] functions.
type RustParser struct{}

// GoParser handles .go files, extracting import paths and Test-prefixed
// functions.
type GoParser struct{}

// PythonParser handles .py and .pyw files, extracting import statements and
// pytest-style test functions.
type PythonParser struct{}

// JavaScriptParser handles JavaScript and TypeScript sources, extracting import
// and require calls along with test cases.
type JavaScriptParser struct{}

// Accepts reports whether path has a Rust .rs extension.
func (p *RustParser) Accepts(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".rs")
}

// Parse records the use imports and test functions found in a Rust file.
func (p *RustParser) Parse(request Request) (SourceInfo, error) {
	info, tokens, err := parseGeneric(request, LanguageRust)
	if err != nil {
		return SourceInfo{}, err
	}
	for index, current := range tokens {
		if current.Kind != tokenIdentifier {
			continue
		}
		inspectRustUse(&info, tokens, index, current)
		inspectRustTest(&info, tokens, index, current)
	}
	return info, nil
}

// inspectRustUse records a "use" path as a dependency.
func inspectRustUse(info *SourceInfo, tokens []token, index int, current token) {
	if current.Text != "use" || index+1 >= len(tokens) {
		return
	}
	name, _ := moduleName(tokens, index+1)
	if name != "" {
		info.Dependencies = append(info.Dependencies, SourceDependency{Kind: Import, Name: name, Range: tokenRange(current)})
	}
}

// inspectRustTest records the function following a "#[test]" attribute as both a
// test and a symbol.
func inspectRustTest(info *SourceInfo, tokens []token, index int, current token) {
	if current.Text != "test" || index < 2 || tokens[index-2].Text != "#" || tokens[index-1].Text != "[" || index+1 >= len(tokens) || tokens[index+1].Text != "]" {
		return
	}
	cursor := index + 2
	for cursor < len(tokens) && tokens[cursor].Kind == tokenNewline {
		cursor++
	}
	if cursor+1 >= len(tokens) || tokens[cursor].Text != "fn" || tokens[cursor+1].Kind != tokenIdentifier {
		return
	}
	name := tokens[cursor+1].Text
	info.Tests = append(info.Tests, TestInfo{Name: name, Framework: "rust", Range: tokenRange(current), Symbol: name})
	info.Symbols = append(info.Symbols, SymbolInfo{Name: name, Kind: "function", Range: tokenRange(tokens[cursor+1])})
}

// Accepts reports whether path has a .go extension.
func (p *GoParser) Accepts(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".go")
}

// Parse records the import paths and Test functions found in a Go file.
func (p *GoParser) Parse(request Request) (SourceInfo, error) {
	info, tokens, err := parseGeneric(request, LanguageGo)
	if err != nil {
		return SourceInfo{}, err
	}
	for index, current := range tokens {
		if current.Kind != tokenIdentifier {
			continue
		}
		if current.Text == "import" {
			inspectGoImport(&info, tokens, index)
		}
		if strings.HasPrefix(current.Text, "Test") && index+1 < len(tokens) && tokens[index+1].Text == "(" {
			info.Tests = append(info.Tests, TestInfo{Name: current.Text, Framework: "go", Range: tokenRange(current), Symbol: current.Text})
			info.Symbols = append(info.Symbols, SymbolInfo{Name: current.Text, Kind: "function", Range: tokenRange(current)})
		}
	}
	return info, nil
}

// inspectGoImport appends the import paths named by a Go import statement,
// handling both the parenthesized group form and a single-path form.
func inspectGoImport(info *SourceInfo, tokens []token, index int) {
	cursor := index + 1
	if cursor < len(tokens) && tokens[cursor].Text == "(" {
		inspectGoImportGroup(info, tokens, cursor)
		return
	}
	for cursor < len(tokens) && tokens[cursor].Kind != tokenNewline {
		if tokens[cursor].Kind == tokenString {
			appendImportPath(info, tokens[cursor])
		}
		cursor++
	}
}

// inspectGoImportGroup appends every string import found inside a parenthesized
// import group, stopping at the matching close parenthesis.
func inspectGoImportGroup(info *SourceInfo, tokens []token, cursor int) {
	depth := 0
	for cursor < len(tokens) {
		if tokens[cursor].Text == "(" {
			depth++
		} else if tokens[cursor].Text == ")" {
			depth--
			if depth == 0 {
				break
			}
		} else if tokens[cursor].Kind == tokenString {
			appendImportPath(info, tokens[cursor])
		}
		cursor++
	}
}

// appendImportPath records a quoted import path as a dependency.
func appendImportPath(info *SourceInfo, current token) {
	info.Dependencies = append(info.Dependencies, SourceDependency{Kind: Import, Name: current.Text, Range: tokenRange(current)})
}

// Accepts reports whether path is a Python .py or .pyw file.
func (p *PythonParser) Accepts(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".py" || extension == ".pyw"
}

// Parse records the imports and pytest-style tests found in a Python file.
func (p *PythonParser) Parse(request Request) (SourceInfo, error) {
	info, tokens, err := parseGeneric(request, LanguagePython)
	if err != nil {
		return SourceInfo{}, err
	}
	for index, current := range tokens {
		if current.Kind != tokenIdentifier {
			continue
		}
		inspectPythonImport(&info, tokens, index, current)
		inspectPythonTest(&info, tokens, index, current)
	}
	return info, nil
}

// inspectPythonImport records an "import" or "from" module path as a runtime
// dependency.
func inspectPythonImport(info *SourceInfo, tokens []token, index int, current token) {
	if (current.Text != "import" && current.Text != "from") || index+1 >= len(tokens) || tokens[index+1].Kind != tokenIdentifier {
		return
	}
	name, _ := moduleName(tokens, index+1)
	if name != "" {
		info.Dependencies = append(info.Dependencies, SourceDependency{Kind: RuntimeImport, Name: name, Range: tokenRange(current)})
	}
}

// inspectPythonTest records a test_ function as both a test and a symbol.
func inspectPythonTest(info *SourceInfo, tokens []token, index int, current token) {
	if (!strings.HasPrefix(current.Text, "test_") && current.Text != "test") || index+1 >= len(tokens) || tokens[index+1].Text != "(" {
		return
	}
	info.Tests = append(info.Tests, TestInfo{Name: current.Text, Framework: "pytest", Range: tokenRange(current), Symbol: current.Text})
	info.Symbols = append(info.Symbols, SymbolInfo{Name: current.Text, Kind: "function", Range: tokenRange(current)})
}

// Accepts reports whether path is a JavaScript or TypeScript source.
func (p *JavaScriptParser) Accepts(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
}

// Parse records the imports, require calls, and tests found in a JavaScript or
// TypeScript file.
func (p *JavaScriptParser) Parse(request Request) (SourceInfo, error) {
	info, tokens, err := parseGeneric(request, languageFor(request.Path))
	if err != nil {
		return SourceInfo{}, err
	}
	for index, current := range tokens {
		if current.Kind != tokenIdentifier {
			continue
		}
		inspectJavaScriptImport(&info, tokens, index, current)
		inspectJavaScriptRequire(&info, tokens, index, current)
		inspectJavaScriptTest(&info, tokens, index, current)
	}
	return info, nil
}

// inspectJavaScriptImport records the first string of an "import" or "from"
// clause as a dependency, trimming leading path punctuation.
func inspectJavaScriptImport(info *SourceInfo, tokens []token, index int, current token) {
	if current.Text != "import" && current.Text != "from" {
		return
	}
	for cursor := index + 1; cursor < len(tokens) && tokens[cursor].Kind != tokenNewline; cursor++ {
		if tokens[cursor].Kind == tokenString {
			info.Dependencies = append(info.Dependencies, SourceDependency{Kind: Import, Name: strings.Trim(tokens[cursor].Text, "./"), Range: tokenRange(tokens[cursor])})
			break
		}
	}
}

// inspectJavaScriptRequire records the string argument of a "require" call as a
// runtime dependency.
func inspectJavaScriptRequire(info *SourceInfo, tokens []token, index int, current token) {
	if current.Text != "require" || index+2 >= len(tokens) || tokens[index+1].Text != "(" || tokens[index+2].Kind != tokenString {
		return
	}
	info.Dependencies = append(info.Dependencies, SourceDependency{Kind: RuntimeImport, Name: strings.Trim(tokens[index+2].Text, "./"), Range: tokenRange(tokens[index+2])})
}

// inspectJavaScriptTest records the string label of a "test" or "it" call as a
// test.
func inspectJavaScriptTest(info *SourceInfo, tokens []token, index int, current token) {
	if (current.Text != "test" && current.Text != "it") || index+2 >= len(tokens) || tokens[index+1].Text != "(" || tokens[index+2].Kind != tokenString {
		return
	}
	info.Tests = append(info.Tests, TestInfo{Name: tokens[index+2].Text, Framework: "javascript", Range: tokenRange(current)})
}

func parseGeneric(request Request, language Language) (SourceInfo, []token, error) {
	data, err := os.ReadFile(request.Path)
	if err != nil {
		return SourceInfo{}, nil, err
	}
	return SourceInfo{Path: filepath.Clean(request.Path), Language: language, Confidence: ConfidenceHigh, ContentHash: hash(data)}, lexAll(data), nil
}

func tokenRange(value token) Range {
	return Range{Start: value.Start, End: value.End}
}
