package source

import (
	"os"
	"path/filepath"
	"strings"
)

type RustParser struct{}
type GoParser struct{}
type PythonParser struct{}
type JavaScriptParser struct{}

func (p *RustParser) Accepts(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".rs")
}

func (p *RustParser) Parse(request Request) (SourceInfo, error) {
	info, tokens, err := parseGeneric(request, LanguageRust)
	if err != nil {
		return SourceInfo{}, err
	}
	for index, current := range tokens {
		if current.Kind != tokenIdentifier {
			continue
		}
		if current.Text == "use" && index+1 < len(tokens) {
			name, _ := moduleName(tokens, index+1)
			if name != "" {
				info.Dependencies = append(info.Dependencies, SourceDependency{Kind: Import, Name: name, Range: tokenRange(current)})
			}
		}
		if current.Text == "test" && index >= 2 && tokens[index-2].Text == "#" && tokens[index-1].Text == "[" && index+1 < len(tokens) && tokens[index+1].Text == "]" {
			cursor := index + 2
			for cursor < len(tokens) && tokens[cursor].Kind == tokenNewline {
				cursor++
			}
			if cursor+1 < len(tokens) && tokens[cursor].Text == "fn" && tokens[cursor+1].Kind == tokenIdentifier {
				name := tokens[cursor+1].Text
				info.Tests = append(info.Tests, TestInfo{Name: name, Framework: "rust", Range: tokenRange(current), Symbol: name})
				info.Symbols = append(info.Symbols, SymbolInfo{Name: name, Kind: "function", Range: tokenRange(tokens[cursor+1])})
			}
		}
	}
	return info, nil
}

func (p *GoParser) Accepts(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".go")
}

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
			cursor := index + 1
			if cursor < len(tokens) && tokens[cursor].Text == "(" {
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
						info.Dependencies = append(info.Dependencies, SourceDependency{Kind: Import, Name: tokens[cursor].Text, Range: tokenRange(tokens[cursor])})
					}
					cursor++
				}
			} else {
				for cursor < len(tokens) {
					if tokens[cursor].Kind == tokenNewline {
						break
					}
					if tokens[cursor].Kind == tokenString {
						info.Dependencies = append(info.Dependencies, SourceDependency{Kind: Import, Name: tokens[cursor].Text, Range: tokenRange(tokens[cursor])})
					}
					cursor++
				}
			}
		}
		if strings.HasPrefix(current.Text, "Test") && index+1 < len(tokens) && tokens[index+1].Text == "(" {
			info.Tests = append(info.Tests, TestInfo{Name: current.Text, Framework: "go", Range: tokenRange(current), Symbol: current.Text})
			info.Symbols = append(info.Symbols, SymbolInfo{Name: current.Text, Kind: "function", Range: tokenRange(current)})
		}
	}
	return info, nil
}

func (p *PythonParser) Accepts(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".py" || extension == ".pyw"
}

func (p *PythonParser) Parse(request Request) (SourceInfo, error) {
	info, tokens, err := parseGeneric(request, LanguagePython)
	if err != nil {
		return SourceInfo{}, err
	}
	for index, current := range tokens {
		if current.Kind != tokenIdentifier {
			continue
		}
		if (current.Text == "import" || current.Text == "from") && index+1 < len(tokens) && tokens[index+1].Kind == tokenIdentifier {
			name, _ := moduleName(tokens, index+1)
			if name != "" {
				info.Dependencies = append(info.Dependencies, SourceDependency{Kind: RuntimeImport, Name: name, Range: tokenRange(current)})
			}
		}
		if (strings.HasPrefix(current.Text, "test_") || current.Text == "test") && index+1 < len(tokens) && tokens[index+1].Text == "(" {
			info.Tests = append(info.Tests, TestInfo{Name: current.Text, Framework: "pytest", Range: tokenRange(current), Symbol: current.Text})
			info.Symbols = append(info.Symbols, SymbolInfo{Name: current.Text, Kind: "function", Range: tokenRange(current)})
		}
	}
	return info, nil
}

func (p *JavaScriptParser) Accepts(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
}

func (p *JavaScriptParser) Parse(request Request) (SourceInfo, error) {
	info, tokens, err := parseGeneric(request, languageFor(request.Path))
	if err != nil {
		return SourceInfo{}, err
	}
	for index, current := range tokens {
		if current.Kind != tokenIdentifier {
			continue
		}
		if current.Text == "import" || current.Text == "from" {
			for cursor := index + 1; cursor < len(tokens) && tokens[cursor].Kind != tokenNewline; cursor++ {
				if tokens[cursor].Kind == tokenString {
					info.Dependencies = append(info.Dependencies, SourceDependency{Kind: Import, Name: strings.Trim(tokens[cursor].Text, "./"), Range: tokenRange(tokens[cursor])})
					break
				}
			}
		}
		if current.Text == "require" && index+2 < len(tokens) && tokens[index+1].Text == "(" && tokens[index+2].Kind == tokenString {
			info.Dependencies = append(info.Dependencies, SourceDependency{Kind: RuntimeImport, Name: strings.Trim(tokens[index+2].Text, "./"), Range: tokenRange(tokens[index+2])})
		}
		if (current.Text == "test" || current.Text == "it") && index+2 < len(tokens) && tokens[index+1].Text == "(" && tokens[index+2].Kind == tokenString {
			info.Tests = append(info.Tests, TestInfo{Name: tokens[index+2].Text, Framework: "javascript", Range: tokenRange(current)})
		}
	}
	return info, nil
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
