package source

import "testing"

// TestLexerTokenStream locks the token stream produced by next for a sample that
// exercises identifiers, numbers, strings, punctuation, comments, unicode, and
// newlines, so the extracted scanning helpers stay behaviour-preserving.
func TestLexerTokenStream(t *testing.T) {
	input := "int x = 42;\n// comment\n\"a\\n\" 'c' && \u00e1 0x1F /* block */\n"
	lexer := &lexer{data: []byte(input)}
	type want struct {
		kind        tokenKind
		text        string
		firstOnLine bool
	}
	wants := []want{
		{tokenIdentifier, "int", false},
		{tokenIdentifier, "x", false},
		{tokenPunctuation, "=", false},
		{tokenNumber, "42", false},
		{tokenPunctuation, ";", false},
		{tokenNewline, "\n", true},
		{tokenNewline, "\n", true},
		{tokenString, "a\n", true},
		{tokenString, "c", false},
		{tokenPunctuation, "&&", false},
		{tokenIdentifier, "\u00e1", false},
		{tokenNumber, "0x1F", false},
		{tokenNewline, "\n", true},
		{tokenEOF, "", true},
	}
	for i, expected := range wants {
		got := lexer.next()
		if got.Kind != expected.kind || got.Text != expected.text || got.FirstOnLine != expected.firstOnLine {
			t.Fatalf("token %d: got {kind:%d text:%q firstOnLine:%v}, want {kind:%d text:%q firstOnLine:%v}",
				i, got.Kind, got.Text, got.FirstOnLine, expected.kind, expected.text, expected.firstOnLine)
		}
	}
}
