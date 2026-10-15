package history

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"
)

func TestBytePrefilterJSONSpellings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		term, raw string
		candidate bool
	}{
		{`"`, `{"text":"say \"hello\""}`, true},
		{`\`, `{"text":"a\\b"}`, true},
		{"\n\t", `{"text":"a\n\tb"}`, true},
		{"a/b/c", `{"text":"a\/b/c"}`, true},
		{"a/b/c", `{"text":"a/b\/c"}`, true},
		{"&<>", `{"text":"&<>"}`, true},
		{"needle", `{"text":"\u006eeedle"}`, true},
		{"needle", `{"text":"n\u0065edle"}`, true},
		{"needle", `{"text":"other\u001b"}`, false},
		{"needle", `{"text":"\ud800\u006eeedle"}`, true},
		{"needle", `{"text":"\udc00\u006eeedle"}`, true},
		{"ss", `{"text":"\u00df"}`, true},
		{"ffi", `{"text":"\ufb03"}`, true},
		{"istanbul", `{"text":"\u0130STANBUL"}`, true},
		{"éneedle", `{"text":"\u00e9need\u006ce"}`, true},
		{"éneedle", `{"text":"other\u00e9"}`, false},
		{"missing", `{"text":"\u00e9\u2022\ud83d\ude00"}`, false},
		{"friction log", `{"text":"Friction LOG"}`, true},
		{"STRASSE", "{\"text\":\"Stra\u00dfe\"}", true},
		{"ffi", "{\"text\":\"\ufb03\"}", true},
		{"istanbul", "{\"text\":\"\u0130STANBUL\"}", true},
		{"\u03c3", "{\"text\":\"\u03c2\"}", true},
		{"\u00e9", `{"text":"\u00e9"}`, true},
		{"\u754c", `{"text":"\u754c"}`, true},
		{"\U0001f600", `{"text":"\ud83d\ude00"}`, true},
		{"missing", `{"text":"ordinary ascii"}`, false},
		{"missing", "{\"text\":\"Stra\u00dfe\"}", false},
	} {
		t.Run(test.term+test.raw, func(t *testing.T) {
			terms, err := compileMatchers([]string{test.term}, Query{})
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := newBytePrefilter(terms).matches(t.Context(), strings.NewReader(test.raw))
			if err != nil || candidate != test.candidate {
				t.Fatalf("candidate=%t want=%t err=%v", candidate, test.candidate, err)
			}
		})
	}
}

func TestBytePrefilterChunkBoundariesAndCancellation(t *testing.T) {
	t.Parallel()
	terms, err := compileMatchers([]string{"first", "needle"}, Query{})
	if err != nil {
		t.Fatal(err)
	}
	filter := newBytePrefilter(terms)
	for split := 1; split < len("needle"); split++ {
		body := "first" + strings.Repeat(" ", hashChunkBytes+filter.overlap-len("first")-split) + "needle"
		candidate, err := filter.matches(t.Context(), strings.NewReader(body))
		if err != nil || !candidate {
			t.Fatalf("split=%d candidate=%t err=%v", split, candidate, err)
		}
	}
	candidate, err := filter.matches(t.Context(), strings.NewReader("first only"))
	if err != nil || candidate {
		t.Fatalf("missing second term: candidate=%t err=%v", candidate, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := filter.matches(ctx, strings.NewReader("first needle")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scan: %v", err)
	}
	if _, err := filter.matches(t.Context(), failingPrefilterReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("failed read: %v", err)
	}
}

type failingPrefilterReader struct{}

func (failingPrefilterReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestBytePrefilterContainsEveryTextMatch(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.String().Draw(t, "text")
		query := Query{CaseSensitive: rapid.Bool().Draw(t, "case sensitive"), Word: rapid.Bool().Draw(t, "word")}
		term := rapid.SampledFrom([]string{text, fold(text), "absent"}).Draw(t, "term")
		terms, err := compileMatchers([]string{term}, query)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := newBytePrefilter(terms).matches(t.Context(), strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		normalized := text
		if !query.CaseSensitive {
			normalized = fold(text)
		}
		if terms[0].matches(text, normalized) && !candidate {
			t.Fatalf("prefilter rejected text=%q term=%q", text, term)
		}
	})
}

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
