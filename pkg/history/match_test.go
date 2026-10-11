package history

import (
	"regexp"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"
)

func TestRegexLiteralAppearsInEveryMatch(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		caseSensitive := rapid.Bool().Draw(t, "case sensitive")
		term := drawRegexTerm(t, 3, !caseSensitive)
		body := rapid.StringOfN(rapid.SampledFrom(regexRunes), 0, 3, -1).Draw(t, "prefix") +
			term.witness +
			rapid.StringOfN(rapid.SampledFrom(regexRunes), 0, 3, -1).Draw(t, "suffix")

		matchers, err := compileMatchers([]string{term.expression}, Query{Regex: true, CaseSensitive: caseSensitive})
		if err != nil {
			t.Fatalf("compile %q: %v", term.expression, err)
		}
		m := matchers[0]
		if !m.pattern.MatchString(body) {
			t.Fatalf("generator built %q for %q, which does not match", body, term.expression)
		}
		if !strings.Contains(fold(body), fold(m.literal)) {
			t.Fatalf("folded body %q lacks folded literal %q of %q", fold(body), fold(m.literal), term.expression)
		}
		if caseSensitive && !strings.Contains(body, m.literal) {
			t.Fatalf("body %q lacks literal %q of %q", body, m.literal, term.expression)
		}
	})
}

func TestRegexLiteralExamples(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		expression    string
		caseSensitive bool
		want          string
	}{
		{expression: "(?:abcd)?xy", want: "xy"},
		{expression: "(?:abcd|e)xy", want: "xy"},
		{expression: "a(?i:bcd)", caseSensitive: true, want: "a"},
	} {
		matchers, err := compileMatchers([]string{test.expression}, Query{Regex: true, CaseSensitive: test.caseSensitive})
		if err != nil {
			t.Fatal(err)
		}
		if got := matchers[0].literal; got != test.want {
			t.Errorf("literal of %q (case sensitive %v) = %q, want %q", test.expression, test.caseSensitive, got, test.want)
		}
	}
}

var regexRunes = []rune("abkKsSK" + "ſßẞσςΣθϑϴİıi .")

type regexTerm struct {
	expression string
	witness    string
}

func drawRegexTerm(t *rapid.T, depth int, foldCase bool) regexTerm {
	if depth == 0 {
		return drawRegexLeaf(t, foldCase)
	}
	switch rapid.SampledFrom([]string{"leaf", "concat", "alternate", "repetition", "flags"}).Draw(t, "kind") {
	case "leaf":
		return drawRegexLeaf(t, foldCase)
	case "concat":
		left, right := drawRegexTerm(t, depth-1, foldCase), drawRegexTerm(t, depth-1, foldCase)
		return regexTerm{expression: left.expression + right.expression, witness: left.witness + right.witness}
	case "alternate":
		left, right := drawRegexTerm(t, depth-1, foldCase), drawRegexTerm(t, depth-1, foldCase)
		witness := left.witness
		if rapid.Bool().Draw(t, "right branch") {
			witness = right.witness
		}
		return regexTerm{expression: "(?:" + left.expression + "|" + right.expression + ")", witness: witness}
	case "repetition":
		return drawRegexRepetition(t, depth-1, foldCase)
	}
	nested := rapid.Bool().Draw(t, "nested fold case")
	flag := "(?-i:"
	if nested {
		flag = "(?i:"
	}
	inner := drawRegexTerm(t, depth-1, nested)
	return regexTerm{expression: flag + inner.expression + ")", witness: inner.witness}
}

func drawRegexRepetition(t *rapid.T, depth int, foldCase bool) regexTerm {
	inner := drawRegexTerm(t, depth, foldCase)
	switch rapid.SampledFrom([]string{"optional", "star", "plus", "repeat", "capture"}).Draw(t, "repetition") {
	case "optional":
		return regexTerm{expression: "(?:" + inner.expression + ")?", witness: inner.witness[:len(inner.witness)*rapid.IntRange(0, 1).Draw(t, "present")]}
	case "star":
		return regexTerm{expression: "(?:" + inner.expression + ")*", witness: strings.Repeat(inner.witness, rapid.IntRange(0, 2).Draw(t, "copies"))}
	case "plus":
		return regexTerm{expression: "(?:" + inner.expression + ")+", witness: strings.Repeat(inner.witness, rapid.IntRange(1, 2).Draw(t, "copies"))}
	case "repeat":
		return regexTerm{expression: "(?:" + inner.expression + "){2}", witness: inner.witness + inner.witness}
	case "capture":
		return regexTerm{expression: "(" + inner.expression + ")", witness: inner.witness}
	}
	return inner
}

func drawRegexLeaf(t *rapid.T, foldCase bool) regexTerm {
	switch rapid.SampledFrom([]string{"literal", "class", "any"}).Draw(t, "leaf kind") {
	case "class":
		member := rapid.SampledFrom([]string{"a", "b"}).Draw(t, "class member")
		return regexTerm{expression: "[ab]", witness: caseVariant(t, member, foldCase)}
	case "any":
		return regexTerm{expression: ".", witness: string(rapid.SampledFrom(regexRunes).Draw(t, "any rune"))}
	}
	literal := string(rapid.SampledFrom(regexRunes).Draw(t, "literal"))
	return regexTerm{expression: regexp.QuoteMeta(literal), witness: caseVariant(t, literal, foldCase)}
}

func caseVariant(t *rapid.T, letter string, foldCase bool) string {
	r := []rune(letter)[0]
	if !foldCase {
		return letter
	}
	variants := []rune{r}
	for other := unicode.SimpleFold(r); other != r; other = unicode.SimpleFold(other) {
		variants = append(variants, other)
	}
	return string(rapid.SampledFrom(variants).Draw(t, "case variant"))
}
