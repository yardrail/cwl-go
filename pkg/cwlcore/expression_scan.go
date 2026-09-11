package cwlcore

import (
	"fmt"
	"unicode/utf8"
)

// scanState is one frame of the expression scanner's state stack.
type scanState int

const (
	// scanText is ordinary literal text outside any fragment.
	scanText scanState = iota

	// scanDollar is the state after a "$" that may still open a fragment.
	scanDollar

	// scanParen is inside a $(...) fragment, one frame per open paren.
	scanParen

	// scanBrace is inside a ${...} fragment, one frame per open brace.
	scanBrace

	// scanSingleQuote is inside a '...' JavaScript string literal.
	scanSingleQuote

	// scanDoubleQuote is inside a "..." JavaScript string literal.
	scanDoubleQuote

	// scanBackslash swallows exactly one following character.
	scanBackslash
)

// scanWindow is the byte range [start, end) of one scanned fragment or escape.
type scanWindow struct {
	start  int
	end    int
	escape bool
}

// exprScanner finds the first expression fragment or escape in a string.
type exprScanner struct {
	src   string
	stack []scanState
	pos   int

	// fragStart is the offset of the '$' that opened the fragment.
	fragStart int

	// escStart is the offset of the most recent '\'.
	escStart int
}

// scanFragment returns the first expression fragment or escape in src.
func scanFragment(src string) (scanWindow, bool, error) {
	scanner := &exprScanner{src: src, stack: []scanState{scanText}, pos: 0, fragStart: 0, escStart: 0}

	for scanner.pos < len(src) {
		char, size := utf8.DecodeRuneInString(src[scanner.pos:])

		window, done := scanner.step(char, size)
		if done {
			return window, true, nil
		}
	}

	return scanWindow{start: 0, end: 0, escape: false}, false, scanner.unterminated()
}

// step consumes one rune and reports a completed window if any.
func (s *exprScanner) step(char rune, size int) (scanWindow, bool) {
	switch s.top() {
	case scanText:
		return s.stepText(char, size)
	case scanBackslash:
		return s.stepBackslash(char, size)
	case scanDollar:
		return s.stepDollar(char, size)
	case scanParen:
		return s.stepGroup(char, '(', ')', size)
	case scanBrace:
		return s.stepGroup(char, '{', '}', size)
	case scanSingleQuote:
		return s.stepQuoted(char, '\'', size)
	case scanDoubleQuote:
		return s.stepQuoted(char, '"', size)
	default:
		s.pos += size

		return scanWindow{start: 0, end: 0, escape: false}, false
	}
}

// stepText scans literal text, where only "$" and "\" are significant.
func (s *exprScanner) stepText(char rune, size int) (scanWindow, bool) {
	switch char {
	case '$':
		s.fragStart = s.pos
		s.push(scanDollar)
	case '\\':
		s.escStart = s.pos
		s.push(scanBackslash)
	default:
	}

	s.pos += size

	return scanWindow{start: 0, end: 0, escape: false}, false
}

// stepBackslash consumes the escaped rune. In literal text, reports the escape window.
// For \$( and \${, the window covers the opening delimiter too.
func (s *exprScanner) stepBackslash(char rune, size int) (scanWindow, bool) {
	s.pop()
	s.pos += size

	if s.top() != scanText {
		return scanWindow{start: 0, end: 0, escape: false}, false
	}

	if char == '$' && s.opensFragment() {
		s.pos++
	}

	return scanWindow{start: s.escStart, end: s.pos, escape: true}, true
}

// opensFragment reports whether the byte at pos would open a fragment.
func (s *exprScanner) opensFragment() bool {
	return s.pos < len(s.src) && (s.src[s.pos] == '(' || s.src[s.pos] == '{')
}

// stepDollar decides whether the "$" just seen opens a fragment.
func (s *exprScanner) stepDollar(char rune, size int) (scanWindow, bool) {
	switch char {
	case '(':
		s.push(scanParen)
	case '{':
		s.push(scanBrace)
	default:
		s.pop()

		return scanWindow{start: 0, end: 0, escape: false}, false
	}

	s.pos += size

	return scanWindow{start: 0, end: 0, escape: false}, false
}

// stepGroup scans inside a fragment, tracking nested delimiters.
func (s *exprScanner) stepGroup(char, openRune, closeRune rune, size int) (scanWindow, bool) {
	state := s.top()
	s.pos += size

	switch char {
	case openRune:
		s.push(state)
	case closeRune:
		s.pop()

		if s.top() == scanDollar {
			return scanWindow{start: s.fragStart, end: s.pos, escape: false}, true
		}
	case '\'':
		s.push(scanSingleQuote)
	case '"':
		s.push(scanDoubleQuote)
	default:
	}

	return scanWindow{start: 0, end: 0, escape: false}, false
}

// stepQuoted scans inside a JavaScript string literal.
func (s *exprScanner) stepQuoted(char, quote rune, size int) (scanWindow, bool) {
	s.pos += size

	switch char {
	case quote:
		s.pop()
	case '\\':
		s.escStart = s.pos - size
		s.push(scanBackslash)
	default:
	}

	return scanWindow{start: 0, end: 0, escape: false}, false
}

// unterminated reports a fragment left open at end of input, if any.
func (s *exprScanner) unterminated() error {
	if len(s.stack) <= 1 {
		return nil
	}

	if len(s.stack) == 2 && (s.stack[1] == scanBackslash || s.stack[1] == scanDollar) {
		return nil
	}

	return fmt.Errorf("%w: unterminated expression at offset %d: %q",
		ErrExpressionSyntax, s.fragStart, s.src[s.fragStart:])
}

// top returns the current state.
func (s *exprScanner) top() scanState {
	return s.stack[len(s.stack)-1]
}

// push enters a nested state.
func (s *exprScanner) push(state scanState) {
	s.stack = append(s.stack, state)
}

// pop leaves the current state.
func (s *exprScanner) pop() {
	if len(s.stack) > 1 {
		s.stack = s.stack[:len(s.stack)-1]
	}
}
