// Package cobrausage recognizes command-line usage errors that cobra returns
// as plain errors.
package cobrausage

import "strings"

// Is reports whether err is a cobra flag, argument-count, or unknown-command
// error. Cobra has no error types for these, so the check matches its messages.
func Is(err error) bool {
	if err == nil {
		return false
	}

	message := err.Error()
	for _, prefix := range []string{
		"unknown flag",
		"unknown shorthand flag",
		"flag needs an argument",
		"invalid argument",
		"unknown command",
	} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}

	return (strings.Contains(message, "accepts ") || strings.Contains(message, "requires at least")) &&
		strings.Contains(message, " arg")
}
