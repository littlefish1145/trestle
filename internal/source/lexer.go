package source

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenIdentifier
	tokenNumber
	tokenString
	tokenChar
	tokenPunctuation
	tokenNewline
)

type token struct {
	Kind        tokenKind
	Text        string
	Start       Position
	End         Position
	FirstOnLine bool
}

type lexer struct {
	data         []byte
	offset       int
	line         int
	column       int
	firstOnLine  bool
	leadingSpace bool
}

func (l *lexer) next() token {
	for l.offset < len(l.data) {
		char := l.data[l.offset]
		if char == '\n' {
			return l.lexNewline()
		}
		if isInlineSpace(char) {
			l.leadingSpace = true
			l.advance()
			continue
		}
		if char == '/' && l.offset+1 < len(l.data) && l.data[l.offset+1] == '/' {
			l.skipLineComment()
			continue
		}
		if char == '/' && l.offset+1 < len(l.data) && l.data[l.offset+1] == '*' {
			l.skipBlockComment()
			continue
		}
		break
	}
	if l.offset >= len(l.data) {
		return token{Kind: tokenEOF, Start: l.position(), End: l.position(), FirstOnLine: l.firstOnLine}
	}
	firstOnLine := l.firstOnLine
	start := l.position()
	char := l.data[l.offset]
	l.firstOnLine = false
	switch {
	case isIdentifierStart(char):
		return l.lexIdentifier(firstOnLine, start)
	case char >= '0' && char <= '9':
		return l.lexNumber(firstOnLine, start)
	case char == '"' || char == '\'':
		return l.lexString(char, firstOnLine, start)
	default:
		return l.lexPunctuation(char, firstOnLine, start)
	}
}

// lexNewline consumes a line feed and returns the newline token, resetting the
// line-start bookkeeping for the line that follows.
func (l *lexer) lexNewline() token {
	start := l.position()
	l.advance()
	l.firstOnLine = true
	l.leadingSpace = false
	return token{Kind: tokenNewline, Text: "\n", Start: start, End: l.position(), FirstOnLine: true}
}

// skipLineComment consumes a "//" comment up to, but not including, the newline
// that ends it, leaving that newline for next to tokenize.
func (l *lexer) skipLineComment() {
	for l.offset < len(l.data) && l.data[l.offset] != '\n' {
		l.advance()
	}
}

// skipBlockComment consumes a "/* ... */" comment, advancing past the closing
// delimiter or to end of input when the comment is unterminated.
func (l *lexer) skipBlockComment() {
	l.advance()
	l.advance()
	for l.offset < len(l.data) {
		if l.data[l.offset] == '*' && l.offset+1 < len(l.data) && l.data[l.offset+1] == '/' {
			l.advance()
			l.advance()
			break
		}
		l.advance()
	}
}

// lexIdentifier consumes a run of identifier characters and returns the
// identifier token spanning it.
func (l *lexer) lexIdentifier(firstOnLine bool, start Position) token {
	begin := l.offset
	for l.offset < len(l.data) && isIdentifierPart(l.data[l.offset]) {
		l.advance()
	}
	return token{Kind: tokenIdentifier, Text: string(l.data[begin:l.offset]), Start: start, End: l.position(), FirstOnLine: firstOnLine}
}

// lexNumber consumes a run of numeric characters and returns the number token
// spanning it.
func (l *lexer) lexNumber(firstOnLine bool, start Position) token {
	begin := l.offset
	for l.offset < len(l.data) && isNumberPart(l.data[l.offset]) {
		l.advance()
	}
	return token{Kind: tokenNumber, Text: string(l.data[begin:l.offset]), Start: start, End: l.position(), FirstOnLine: firstOnLine}
}

// lexString consumes a quoted string, decoding the common backslash escapes, and
// returns the string token whose Text is the unquoted value.
func (l *lexer) lexString(quote byte, firstOnLine bool, start Position) token {
	l.advance()
	var value strings.Builder
	for l.offset < len(l.data) {
		current := l.data[l.offset]
		if current == quote {
			l.advance()
			break
		}
		if current == '\\' && l.offset+1 < len(l.data) {
			l.advance()
			escaped := l.data[l.offset]
			l.advance()
			switch escaped {
			case 'n':
				value.WriteByte('\n')
			case 'r':
				value.WriteByte('\r')
			case 't':
				value.WriteByte('\t')
			case '\\':
				value.WriteByte('\\')
			case '"':
				value.WriteByte('"')
			case '\'':
				value.WriteByte('\'')
			default:
				value.WriteByte(escaped)
			}
			continue
		}
		value.WriteByte(current)
		l.advance()
	}
	return token{Kind: tokenString, Text: value.String(), Start: start, End: l.position(), FirstOnLine: firstOnLine}
}

// lexPunctuation consumes a punctuation token: a recognised two-character
// operator, or a single rune that is classified as an identifier when it is a
// letter and as punctuation otherwise.
func (l *lexer) lexPunctuation(char byte, firstOnLine bool, start Position) token {
	if (char == '&' || char == '|' || char == '=' || char == '!' || char == '<' || char == '>') && l.offset+1 < len(l.data) && l.data[l.offset+1] == char {
		l.advance()
		l.advance()
		text := string([]byte{char, char})
		return token{Kind: tokenPunctuation, Text: text, Start: start, End: l.position(), FirstOnLine: firstOnLine}
	}
	l.advance()
	runeValue, _ := utf8.DecodeRune(l.data[start.Offset:])
	if unicode.IsLetter(runeValue) {
		return token{Kind: tokenIdentifier, Text: string(runeValue), Start: start, End: l.position(), FirstOnLine: firstOnLine}
	}
	return token{Kind: tokenPunctuation, Text: string(runeValue), Start: start, End: l.position(), FirstOnLine: firstOnLine}
}

func (l *lexer) position() Position {
	return Position{Offset: l.offset, Line: l.line, Column: l.column}
}

func (l *lexer) advance() {
	if l.offset >= len(l.data) {
		return
	}
	if l.data[l.offset] == '\n' {
		l.line++
		l.column = 1
		l.offset++
		return
	}
	_, size := utf8.DecodeRune(l.data[l.offset:])
	l.offset += size
	l.column++
}

// isInlineSpace reports whether char is horizontal whitespace that separates
// tokens without producing a token of its own.
func isInlineSpace(char byte) bool {
	return char == '\r' || char == ' ' || char == '\t' || char == '\v' || char == '\f'
}

func isIdentifierStart(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= utf8.RuneSelf
}

func isIdentifierPart(char byte) bool {
	return isIdentifierStart(char) || char >= '0' && char <= '9'
}

// isNumberPart reports whether char may appear inside a numeric literal: a
// digit, decimal point, sign, or hexadecimal digit.
func isNumberPart(char byte) bool {
	return char == '.' || char == '+' || char == '-' || char >= '0' && char <= '9' || isHexDigit(char)
}

// isHexDigit reports whether char is a hexadecimal digit or the x/X base prefix
// that introduces one.
func isHexDigit(char byte) bool {
	return char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F' || char == 'x' || char == 'X'
}
