package source

import (
	"os"
	"path/filepath"
	"strings"
)

type CFamilyParser struct{}

func (p *CFamilyParser) Accepts(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".c++", ".hpp", ".hh", ".hxx", ".inl", ".ipp", ".m", ".mm", ".cu", ".ixx", ".cppm":
		return true
	default:
		return false
	}
}

type conditionBranch struct {
	condition Condition
	active    bool
	known     bool
}

type conditionFrame struct {
	parentActive bool
	parentKnown  bool
	branches     []conditionBranch
}

func (p *CFamilyParser) Parse(request Request) (SourceInfo, error) {
	data, err := os.ReadFile(request.Path)
	if err != nil {
		return SourceInfo{}, err
	}
	language := languageFor(request.Path)
	if request.Language != "" {
		language = request.Language
	}
	info := SourceInfo{Path: filepath.Clean(request.Path), Language: language, Confidence: ConfidenceHigh, ContentHash: hash(data)}
	tokens := lexAll(data)
	frames := make([]conditionFrame, 0)
	confidence := ConfidenceHigh
	for index := 0; index < len(tokens); index++ {
		current := tokens[index]
		if current.Kind == tokenEOF {
			break
		}
		if current.FirstOnLine && current.Text == "#" {
			directive, next := directiveName(tokens, index+1)
			if directive == "" {
				continue
			}
			line, after := readDirectiveLine(tokens, next)
			switch directive {
			case "if", "ifdef", "ifndef":
				condition := Condition{Kind: ConditionTrue}
				switch directive {
				case "ifdef":
					condition = identifierCondition(line, false)
				case "ifndef":
					condition = Condition{Kind: ConditionNot, Children: []Condition{identifierCondition(line, true)}}
				default:
					condition = parseCondition(line)
				}
				active, known := condition.Evaluate(requestDefineSet(request.Defines))
				parentActive, parentKnown := directiveActive(frames)
				frames = append(frames, conditionFrame{parentActive: parentActive, parentKnown: parentKnown, branches: []conditionBranch{{condition: condition, active: parentActive && active, known: parentKnown && known}}})
				if !known {
					confidence = ConfidenceMedium
				}
			case "elif":
				if len(frames) == 0 {
					continue
				}
				frame := &frames[len(frames)-1]
				knownBefore := false
				for _, branch := range frame.branches {
					knownBefore = knownBefore || branch.known
				}
				condition := parseCondition(line)
				active, known := condition.Evaluate(requestDefineSet(request.Defines))
				branch := conditionBranch{condition: condition, active: false, known: frame.parentKnown && known}
				if !knownBefore {
					branch.active = frame.parentActive && active
				}
				frame.branches = append(frame.branches, branch)
				if !known {
					confidence = ConfidenceMedium
				}
			case "else":
				if len(frames) == 0 {
					continue
				}
				frame := &frames[len(frames)-1]
				knownBefore := false
				taken := false
				elseCondition := Condition{Kind: ConditionTrue}
				for _, branch := range frame.branches {
					knownBefore = knownBefore || branch.known
					taken = taken || branch.active
					if branch.condition.Kind != ConditionTrue {
						elseCondition = Condition{Kind: ConditionOr, Children: []Condition{elseCondition, branch.condition}}
					}
				}
				if len(elseCondition.Children) > 0 {
					elseCondition = Condition{Kind: ConditionNot, Children: []Condition{elseCondition}}
				}
				frame.branches = append(frame.branches, conditionBranch{condition: elseCondition, active: frame.parentActive && !taken, known: frame.parentKnown && knownBefore})
			case "endif":
				if len(frames) > 0 {
					frames = frames[:len(frames)-1]
				}
			case "include", "include_next":
				dependency, dynamic := parseInclude(line)
				if dependency == nil {
					break
				}
				dependency.Range = Range{Start: current.Start, End: line[len(line)-1].End}
				dependency.Condition = currentCondition(frames)
				if dependency.Condition.Kind != ConditionTrue {
					if _, known := dependency.Condition.Evaluate(requestDefineSet(request.Defines)); !known {
						dependency.Optional = true
					}
				}
				if dynamic {
					dependency.Kind = DynamicInclude
					dependency.Name = ""
					confidence = ConfidenceLow
				}
				info.Dependencies = append(info.Dependencies, *dependency)
			}
			index = after - 1
			continue
		}
		inspectCTokens(&info, tokens, index)
	}
	if len(frames) > 0 {
		info.ParseError = "unterminated conditional directive"
		confidence = ConfidenceLow
	}
	info.Confidence = confidence
	return info, nil
}

func lexAll(data []byte) []token {
	scanner := lexer{data: data, line: 1, column: 1, firstOnLine: true}
	tokens := make([]token, 0, len(data)/4+1)
	for {
		current := scanner.next()
		tokens = append(tokens, current)
		if current.Kind == tokenEOF {
			return tokens
		}
	}
}

func directiveName(tokens []token, index int) (string, int) {
	for index < len(tokens) && (tokens[index].Kind == tokenNewline || tokens[index].Kind == tokenEOF) {
		return "", index
	}
	if index >= len(tokens) || tokens[index].Kind != tokenIdentifier {
		return "", index
	}
	return tokens[index].Text, index + 1
}

func readDirectiveLine(tokens []token, index int) ([]token, int) {
	result := make([]token, 0)
	for index < len(tokens) {
		current := tokens[index]
		if current.Kind == tokenEOF {
			return result, index
		}
		if current.Kind == tokenNewline {
			if len(result) > 0 && result[len(result)-1].Text == "\\" {
				index++
				continue
			}
			return result, index + 1
		}
		if current.FirstOnLine && current.Text == "#" {
			return result, index
		}
		result = append(result, current)
		index++
	}
	return result, index
}

func directiveActive(frames []conditionFrame) (bool, bool) {
	active, known := true, true
	for _, frame := range frames {
		if len(frame.branches) == 0 {
			continue
		}
		branch := frame.branches[len(frame.branches)-1]
		active = active && branch.active
		known = known && branch.known
	}
	return active, known
}

func currentCondition(frames []conditionFrame) Condition {
	result := Condition{Kind: ConditionTrue}
	for _, frame := range frames {
		if len(frame.branches) == 0 {
			continue
		}
		branch := frame.branches[len(frame.branches)-1]
		if branch.condition.Kind == ConditionTrue {
			continue
		}
		result = combineAnd(result, branch.condition)
	}
	return result
}

func combineAnd(left, right Condition) Condition {
	if left.Kind == ConditionTrue {
		return right
	}
	if right.Kind == ConditionTrue {
		return left
	}
	return Condition{Kind: ConditionAnd, Children: []Condition{left, right}}
}

func identifierCondition(tokens []token, defined bool) Condition {
	if len(tokens) == 0 || tokens[0].Kind != tokenIdentifier {
		return Condition{Kind: ConditionUnknown}
	}
	kind := ConditionIdentifier
	if defined {
		kind = ConditionDefined
	}
	return Condition{Kind: kind, Value: tokens[0].Text}
}

func parseCondition(tokens []token) Condition {
	if len(tokens) == 0 {
		return Condition{Kind: ConditionUnknown}
	}
	parser := &conditionParser{tokens: tokens}
	result := parser.parseOr()
	if parser.index < len(tokens) {
		return Condition{Kind: ConditionUnknown}
	}
	return result
}

type conditionParser struct {
	tokens []token
	index  int
}

func (p *conditionParser) parseOr() Condition {
	result := p.parseAnd()
	for p.match("||") {
		result = Condition{Kind: ConditionOr, Children: []Condition{result, p.parseAnd()}}
	}
	return result
}

func (p *conditionParser) parseAnd() Condition {
	result := p.parseEquality()
	for p.match("&&") {
		result = Condition{Kind: ConditionAnd, Children: []Condition{result, p.parseEquality()}}
	}
	return result
}

func (p *conditionParser) parseEquality() Condition {
	result := p.parseUnary()
	for {
		if p.match("==") {
			left := p.parseUnary()
			if len(left.Children) > 0 {
				left = left.Children[0]
			}
			right := p.parseUnary()
			result = Condition{Kind: ConditionEqual, Value: left.Value, Children: []Condition{right}}
		} else if p.match("!=") {
			left := p.parseUnary()
			if len(left.Children) > 0 {
				left = left.Children[0]
			}
			right := p.parseUnary()
			result = Condition{Kind: ConditionNotEqual, Value: left.Value, Children: []Condition{right}}
		} else {
			return result
		}
	}
}

func (p *conditionParser) parseUnary() Condition {
	if p.match("!") {
		return Condition{Kind: ConditionNot, Children: []Condition{p.parseUnary()}}
	}
	if p.match("(") {
		result := p.parseOr()
		p.match(")")
		return result
	}
	if p.index >= len(p.tokens) {
		return Condition{Kind: ConditionUnknown}
	}
	current := p.tokens[p.index]
	p.index++
	if current.Text != "defined" {
		return Condition{Kind: ConditionIdentifier, Value: current.Text}
	}
	hasParentheses := p.match("(")
	if p.index >= len(p.tokens) || p.tokens[p.index].Kind != tokenIdentifier {
		return Condition{Kind: ConditionUnknown}
	}
	value := p.tokens[p.index].Text
	p.index++
	if hasParentheses {
		p.match(")")
	}
	return Condition{Kind: ConditionDefined, Value: value}
}

func (p *conditionParser) match(text string) bool {
	if p.index < len(p.tokens) && p.tokens[p.index].Text == text {
		p.index++
		return true
	}
	return false
}

func parseInclude(tokens []token) (*SourceDependency, bool) {
	if len(tokens) == 0 {
		return nil, true
	}
	if tokens[0].Kind == tokenString {
		return &SourceDependency{Kind: LocalInclude, Name: tokens[0].Text}, false
	}
	if tokens[0].Text == "<" {
		for index, current := range tokens {
			if current.Text == ">" {
				return &SourceDependency{Kind: SystemInclude, Name: joinHeaderTokens(tokens[1:index])}, false
			}
		}
		return nil, true
	}
	return &SourceDependency{Kind: DynamicInclude}, true
}

func joinHeaderTokens(tokens []token) string {
	var result strings.Builder
	for _, current := range tokens {
		if current.Kind == tokenIdentifier || current.Kind == tokenNumber || current.Text == "." || current.Text == "/" || current.Text == "-" || current.Text == ":" {
			result.WriteString(current.Text)
		}
	}
	return result.String()
}

func requestDefineSet(defines []string) map[string]bool {
	result := make(map[string]bool, len(defines))
	for _, define := range defines {
		name := define
		if offset := strings.IndexByte(name, '='); offset >= 0 {
			name = name[:offset]
		}
		result[name] = true
	}
	return result
}

func inspectCTokens(info *SourceInfo, tokens []token, index int) {
	current := tokens[index]
	if current.Kind != tokenIdentifier {
		return
	}
	if index+1 < len(tokens) && tokens[index+1].Text == "(" {
		switch current.Text {
		case "main", "WinMain", "wWinMain":
			kind := "Executable"
			if current.Text != "main" {
				kind = "WindowsExecutable"
			}
			info.EntryPoints = append(info.EntryPoints, EntryPoint{Name: current.Text, Kind: kind, Range: Range{Start: current.Start, End: tokens[index+1].End}})
		case "TEST", "TEST_CASE":
			framework := "gtest"
			if current.Text == "TEST_CASE" {
				framework = "catch2/doctest"
			}
			if test, ok := parseMacroTest(tokens, index, framework); ok {
				info.Tests = append(info.Tests, test)
			}
		}
	}
	if index > 0 && tokens[index-1].Kind == tokenIdentifier {
		switch tokens[index-1].Text {
		case "class", "struct", "namespace", "enum", "union", "module":
			info.Symbols = append(info.Symbols, SymbolInfo{Name: current.Text, Kind: tokens[index-1].Text, Range: Range{Start: current.Start, End: current.End}})
		}
	}
	if current.Text == "import" {
		inspectImport(info, tokens, index)
	}
	if current.Text == "export" && index+1 < len(tokens) && tokens[index+1].Text == "module" {
		inspectModule(info, tokens, index)
	}
	if current.Text == "module" {
		inspectModule(info, tokens, index)
	}
	if strings.Contains(strings.ToLower(current.Text), "generated") {
		info.Generated = append(info.Generated, GeneratedHint{Kind: "GeneratedHint", Target: current.Text, Range: Range{Start: current.Start, End: current.End}})
	}
}

func parseMacroTest(tokens []token, index int, framework string) (TestInfo, bool) {
	index += 2
	parts := make([]string, 0, 2)
	depth := 1
	for index < len(tokens) && depth > 0 {
		if tokens[index].Text == "(" {
			depth++
		} else if tokens[index].Text == ")" {
			depth--
			if depth == 0 {
				break
			}
		}
		if depth == 1 && (tokens[index].Kind == tokenIdentifier || tokens[index].Kind == tokenString) {
			parts = append(parts, tokens[index].Text)
		}
		index++
	}
	if len(parts) == 0 {
		return TestInfo{}, false
	}
	return TestInfo{Name: strings.Join(parts, "."), Framework: framework, Range: Range{Start: tokens[index-len(parts)-1].Start, End: tokens[index].End}}, true
}

func inspectImport(info *SourceInfo, tokens []token, index int) {
	if info.Module == nil {
		info.Module = &ModuleInfo{}
	}
	cursor := index + 1
	if cursor < len(tokens) && tokens[cursor].Text == "module" {
		cursor++
	}
	var name string
	if cursor < len(tokens) && tokens[cursor].Text == ":" {
		name = ":"
		cursor++
	}
	module, _ := moduleName(tokens, cursor)
	name += module
	if name != "" {
		info.Module.Imports = append(info.Module.Imports, name)
	}
}

func inspectModule(info *SourceInfo, tokens []token, index int) {
	cursor := index
	if tokens[cursor].Text == "export" {
		cursor++
	}
	if cursor >= len(tokens) || tokens[cursor].Kind != tokenIdentifier || tokens[cursor].Text != "module" {
		return
	}
	cursor++
	if cursor < len(tokens) && tokens[cursor].Text == ";" {
		return
	}
	if info.Module == nil {
		info.Module = &ModuleInfo{}
	}
	if cursor >= len(tokens) {
		return
	}
	name, next := moduleName(tokens, cursor)
	if name == "" {
		return
	}
	if cursor == index+1 || tokens[index].Text == "module" {
		info.Module.Provides = name
		info.Module.IsInterface = strings.EqualFold(filepath.Ext(info.Path), ".ixx") || strings.EqualFold(filepath.Ext(info.Path), ".cppm")
		info.Module.IsPartition = strings.Contains(name, ":")
	}
	_ = next
}

func moduleName(tokens []token, index int) (string, int) {
	if index >= len(tokens) || tokens[index].Kind != tokenIdentifier {
		return "", index
	}
	var result strings.Builder
	result.WriteString(tokens[index].Text)
	index++
	for index+1 < len(tokens) && (tokens[index].Text == ":" || tokens[index].Text == "." || tokens[index].Text == "-") && tokens[index+1].Kind == tokenIdentifier {
		result.WriteString(tokens[index].Text)
		result.WriteString(tokens[index+1].Text)
		index += 2
	}
	return result.String(), index
}
