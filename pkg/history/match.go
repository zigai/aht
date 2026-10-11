package history

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// matcher finds one query term. literal is the text every match contains,
// normalized like the column it is compared with (folded unless case
// sensitive); it is empty when a regular expression has no required literal.
// pattern is set for regular expressions; word requires literal matches to sit
// between word boundaries.
type matcher struct {
	literal       string
	pattern       *regexp.Regexp
	word          bool
	caseSensitive bool
}

func compileMatchers(terms []string, q Query) ([]matcher, error) {
	matchers := make([]matcher, 0, len(terms))
	for _, term := range terms {
		m := matcher{literal: term, pattern: nil, word: q.Word, caseSensitive: q.CaseSensitive}
		if q.Regex {
			expression := term
			if q.Word {
				expression = `\b(?:` + term + `)\b`
			}
			if !q.CaseSensitive {
				expression = "(?i)" + expression
			}
			parsed, err := syntax.Parse(expression, syntax.Perl)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid regular expression %q: %w", ErrInvalidQuery, term, err)
			}
			pattern, err := regexp.Compile(expression)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid regular expression %q: %w", ErrInvalidQuery, term, err)
			}
			m.pattern = pattern
			m.word = false
			m.literal = requiredLiteral(parsed, q.CaseSensitive)
		}
		if !q.CaseSensitive {
			m.literal = fold(m.literal)
		}
		matchers = append(matchers, m)
	}
	return matchers, nil
}

// requiredLiteral returns a literal contained in every match of re, preferring
// the longest one so index prefilters stay selective. Case-insensitive literals
// cannot prefilter a case-sensitive body column.
func requiredLiteral(re *syntax.Regexp, caseSensitive bool) string {
	switch re.Op {
	case syntax.OpLiteral:
		if caseSensitive && re.Flags&syntax.FoldCase != 0 {
			return ""
		}
		return string(re.Rune)
	case syntax.OpCapture, syntax.OpPlus:
		return requiredLiteral(re.Sub[0], caseSensitive)
	case syntax.OpRepeat:
		if re.Min > 0 {
			return requiredLiteral(re.Sub[0], caseSensitive)
		}
	case syntax.OpConcat:
		longest := ""
		for _, sub := range re.Sub {
			if literal := requiredLiteral(sub, caseSensitive); utf8.RuneCountInString(literal) > utf8.RuneCountInString(longest) {
				longest = literal
			}
		}
		return longest
	case syntax.OpNoMatch, syntax.OpEmptyMatch, syntax.OpCharClass, syntax.OpAnyCharNotNL, syntax.OpAnyChar, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpStar, syntax.OpQuest, syntax.OpAlternate:
	}
	return ""
}

// verifies reports whether index candidates found through the literal still need
// this matcher's full check.
func (m matcher) verifies() bool { return m.pattern != nil || m.word }

// matches reports whether body, whose normalized form is normalized, contains
// a match.
func (m matcher) matches(body, normalized string) bool {
	_, _, ok := m.first(body, normalized)
	return ok
}

// first returns the rune offset and rune length in body of the first match.
func (m matcher) first(body, normalized string) (int, int, bool) {
	if m.pattern != nil {
		location := m.pattern.FindStringIndex(body)
		if location == nil {
			return 0, 0, false
		}
		return utf8.RuneCountInString(body[:location[0]]), utf8.RuneCountInString(body[location[0]:location[1]]), true
	}
	offset := literalIndex(normalized, m.literal, m.word, 0)
	if offset < 0 {
		return 0, 0, false
	}
	anchor := utf8.RuneCountInString(normalized[:offset])
	if !m.caseSensitive {
		anchor = foldRuneIndex(body, anchor)
	}
	return anchor, utf8.RuneCountInString(m.literal), true
}

// literalIndex finds needle in text at or after from, honoring word boundaries.
func literalIndex(text, needle string, word bool, from int) int {
	for from <= len(text) {
		offset := strings.Index(text[from:], needle)
		if offset < 0 {
			return -1
		}
		offset += from
		if !word || wordBoundary(text, offset, offset+len(needle)) {
			return offset
		}
		_, size := utf8.DecodeRuneInString(text[offset:])
		from = offset + max(size, 1)
	}
	return -1
}

func wordBoundary(text string, start, end int) bool {
	if before, _ := utf8.DecodeLastRuneInString(text[:start]); start > 0 && wordRune(before) {
		return false
	}
	if after, _ := utf8.DecodeRuneInString(text[end:]); end < len(text) && wordRune(after) {
		return false
	}
	return true
}

func wordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }

// spans returns every match of the matchers in text as ordered, merged byte
// ranges of text.
func spans(text string, matchers []matcher) []Span {
	found := make([]Span, 0, len(matchers))
	for _, m := range matchers {
		found = append(found, m.spans(text)...)
	}
	if len(found) == 0 {
		return nil
	}
	slices.SortFunc(found, func(a, b Span) int {
		if a.Start != b.Start {
			return a.Start - b.Start
		}
		return b.End - a.End
	})
	merged := found[:1]
	for _, span := range found[1:] {
		last := &merged[len(merged)-1]
		if span.Start <= last.End {
			last.End = max(last.End, span.End)
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

func (m matcher) spans(text string) []Span {
	if m.pattern != nil {
		var found []Span
		for _, location := range m.pattern.FindAllStringIndex(text, -1) {
			if location[1] > location[0] {
				found = append(found, Span{Start: location[0], End: location[1]})
			}
		}
		return found
	}
	if m.literal == "" {
		return nil
	}
	normalized, starts := text, []int(nil)
	if !m.caseSensitive {
		normalized, starts = foldWithOffsets(text)
	}
	var found []Span
	for from := 0; ; {
		offset := literalIndex(normalized, m.literal, m.word, from)
		if offset < 0 {
			return found
		}
		end := offset + len(m.literal)
		if starts == nil {
			found = append(found, Span{Start: offset, End: end})
		} else {
			found = append(found, Span{Start: starts[offset], End: originalEnd(text, starts, end)})
		}
		from = end
	}
}
