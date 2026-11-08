package agentstate

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	matchAll matchMode = iota
	matchAny
	matchNone
)

var matcherSpecs = []matcherSpec{
	{
		name:        "all",
		title:       false,
		regex:       false,
		mode:        matchAll,
		values:      func(r Rule) []string { return r.All },
		valueFormat: "missing literal %q",
		countFormat: "all %d literals matched",
	},
	{
		name:        "any",
		title:       false,
		regex:       false,
		mode:        matchAny,
		values:      func(r Rule) []string { return r.Any },
		valueFormat: "matched literal %q",
		countFormat: "none of %d literals matched",
	},
	{
		name:        "none",
		title:       false,
		regex:       false,
		mode:        matchNone,
		values:      func(r Rule) []string { return r.None },
		valueFormat: "matched excluded literal %q",
		countFormat: "none of %d excluded literals matched",
	},
	{
		name:        "regex_all",
		title:       false,
		regex:       true,
		mode:        matchAll,
		values:      func(r Rule) []string { return r.RegexAll },
		valueFormat: "regular expression %q did not match",
		countFormat: "all %d regular expressions matched",
	},
	{
		name:        "regex_any",
		title:       false,
		regex:       true,
		mode:        matchAny,
		values:      func(r Rule) []string { return r.RegexAny },
		valueFormat: "matched regular expression %q",
		countFormat: "none of %d regular expressions matched",
	},
	{
		name:        "regex_none",
		title:       false,
		regex:       true,
		mode:        matchNone,
		values:      func(r Rule) []string { return r.RegexNone },
		valueFormat: "matched excluded regular expression %q",
		countFormat: "none of %d excluded regular expressions matched",
	},
	{
		name:        "title_any",
		title:       true,
		regex:       false,
		mode:        matchAny,
		values:      func(r Rule) []string { return r.TitleAny },
		valueFormat: "matched title literal %q",
		countFormat: "none of %d title literals matched",
	},
	{
		name:        "title_regex_any",
		title:       true,
		regex:       true,
		mode:        matchAny,
		values:      func(r Rule) []string { return r.TitleRegexAny },
		valueFormat: "matched title regular expression %q",
		countFormat: "none of %d title regular expressions matched",
	},
}

type matchMode uint8

type matcherSpec struct {
	name        string
	title       bool
	regex       bool
	mode        matchMode
	values      func(Rule) []string
	valueFormat string
	countFormat string
}

type matcher struct {
	name        string
	title       bool
	mode        matchMode
	values      []string
	test        func(text string, index int) bool
	valueFormat string
	countFormat string
}

func compileMatcher(spec matcherSpec, values []string, caseSensitive bool) (matcher, error) {
	compiled := matcher{
		name:        spec.name,
		title:       spec.title,
		mode:        spec.mode,
		values:      values,
		test:        nil,
		valueFormat: spec.valueFormat,
		countFormat: spec.countFormat,
	}

	if !spec.regex {
		literals := make([]string, len(values))
		for index, value := range values {
			literals[index] = normalizedLiteral(value, caseSensitive)
		}

		compiled.test = func(text string, index int) bool {
			return strings.Contains(text, literals[index])
		}

		return compiled, nil
	}

	expressions := make([]*regexp.Regexp, len(values))
	for index, value := range values {
		expression, err := regexp.Compile(ruleRegexExpression(value, caseSensitive))
		if err != nil {
			return matcher{}, fmt.Errorf("compiling regular expression %q: %w", value, err)
		}

		expressions[index] = expression
	}

	compiled.test = func(text string, index int) bool {
		return expressions[index].MatchString(text)
	}

	return compiled, nil
}

func (m matcher) check(text string) (bool, int) {
	for index := range m.values {
		hit := m.test(text, index)

		switch m.mode {
		case matchAll:
			if !hit {
				return false, index
			}
		case matchAny:
			if hit {
				return true, index
			}
		case matchNone:
			if hit {
				return false, index
			}
		}
	}

	// No single value decided the outcome.
	return m.mode != matchAny, -1
}

func (m matcher) describe(index int) string {
	if index >= 0 {
		return fmt.Sprintf(m.valueFormat, m.values[index])
	}

	return fmt.Sprintf(m.countFormat, len(m.values))
}

func normalizedLiteral(value string, caseSensitive bool) string {
	if caseSensitive {
		return value
	}

	return strings.ToLower(value)
}

func ruleRegexExpression(expression string, caseSensitive bool) string {
	if caseSensitive {
		return expression
	}

	return "(?i:" + expression + ")"
}
