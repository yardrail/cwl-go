package cwlcore

import (
	"fmt"
	"unicode/utf8"
)

// scanState is one frame of the expression scanner's state stack.
//
// A stack rather than a counter is what lets the scanner honour the spec's
// requirement that it "must permit any syntactically valid Javascript and
// account for nesting of parenthesis or braces and that strings that may
// contain parenthesis or braces": a quote frame suspends delimiter counting
// until the matching quote, and a backslash frame swallows the next character
// wherever it appears.
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

// scanWindow is the half-open byte range [start, end) of one scanned fragment.
// escape distinguishes the two kinds the scanner reports: an expression
// fragment, where src[start] is '$' and src[end-1] closes it, and a backslash
// escape, where src[start] is '\' and src[end-1] is the escaped character.
type scanWindow struct {
	start  int
	end    int
	escape bool
}

// exprScanner finds the first fragment in a string. It is single use: callers
// go through scanFragment, which rescans the remaining text after each hit,
// mirroring the reference implementation.
type exprScanner struct {
	src   string
	stack []scanState
	pos   int

	// fragStart is the offset of the '$' that opened the innermost fragment.
	fragStart int

	// escStart is the offset of the most recent '\'.
	escStart int
}

// scanFragment returns the first expression fragment or backslash escape in
// src. found is false when src holds neither, and an error reports an
// unterminated fragment — the scanner never runs past the end of the string
// looking for a delimiter that is not there.
func scanFragment(src string) (scanWindow, bool, error) {
	scanner := &exprScanner{src: src, stack: []scanState{scanText}}

	for scanner.pos < len(src) {
		char, size := utf8.DecodeRuneInString(src[scanner.pos:])

		window, done := scanner.step(char, size)
		if done {
			return window, true, nil
		}
	}

	return scanWindow{}, false, scanner.unterminated()
}

// step consumes one rune in the current state and reports a completed window.
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

		return scanWindow{}, false
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

	return scanWindow{}, false
}

// stepBackslash consumes the escaped rune. An escape that lands back in
// literal text is itself reported, so the interpolator can apply the spec's
// escaping rules to it; one nested in a string literal is merely swallowed.
//
// The window over "\$(" and "\${" covers the opening delimiter as well as the
// dollar, because the spec escapes the pair — "The substrings \$( and \${ are
// replaced by $( and ${ respectively" — and because leaving the delimiter in
// the unscanned remainder would let the next pass mistake it for a fragment.
func (s *exprScanner) stepBackslash(char rune, size int) (scanWindow, bool) {
	s.pop()
	s.pos += size

	if s.top() != scanText {
		return scanWindow{}, false
	}

	if char == '$' && s.opensFragment() {
		s.pos++
	}

	return scanWindow{start: s.escStart, end: s.pos, escape: true}, true
}

// opensFragment reports whether the byte at the scanner's position would open
// a fragment.
func (s *exprScanner) opensFragment() bool {
	return s.pos < len(s.src) && (s.src[s.pos] == '(' || s.src[s.pos] == '{')
}

// stepDollar decides whether the "$" just seen opens a fragment. If it does
// not, the frame is dropped without consuming the rune, so a "$" followed by
// another "$" still opens on the second one.
func (s *exprScanner) stepDollar(char rune, size int) (scanWindow, bool) {
	switch char {
	case '(':
		s.push(scanParen)
	case '{':
		s.push(scanBrace)
	default:
		s.pop()

		return scanWindow{}, false
	}

	s.pos += size

	return scanWindow{}, false
}

// stepGroup scans inside a fragment, tracking nested delimiters of the same
// kind and handing off to a quote frame at a string literal. Delimiters of the
// other kind need no tracking: JavaScript cannot close a paren with a brace,
// so an unbalanced one inside the fragment is a syntax error the engine
// reports, not something the scanner must anticipate.
func (s *exprScanner) stepGroup(char, openRune, closeRune rune, size int) (scanWindow, bool) {
	state := s.top()
	s.pos += size

	switch char {
	case openRune:
		s.push(state)
	case closeRune:
		s.pop()

		if s.top() == scanDollar {
			return scanWindow{start: s.fragStart, end: s.pos}, true
		}
	case '\'':
		s.push(scanSingleQuote)
	case '"':
		s.push(scanDoubleQuote)
	default:
	}

	return scanWindow{}, false
}

// stepQuoted scans inside a JavaScript string literal, where delimiters are
// inert and only the closing quote and a backslash matter.
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

	return scanWindow{}, false
}

// unterminated reports the fragment left open at end of input, if any.
//
// A trailing lone "$" or "\" is not an error: neither can open a fragment on
// its own, and both are ordinary literal characters at the end of a string.
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

// pop leaves the current state, never emptying the stack: the outermost
// scanText frame is permanent.
func (s *exprScanner) pop() {
	if len(s.stack) > 1 {
		s.stack = s.stack[:len(s.stack)-1]
	}
}
