//go:build linux

package service

import (
	"fmt"
	"strconv"
	"strings"
)

// Decode the argument format emitted by RenderSystemdUnit, including quoted
// paths and literal %% specifiers. Unknown syntax is rejected without writing.
func installedArguments(content []byte) ([]string, error) {
	var command string
	for line := range strings.Lines(string(content)) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			if command != "" {
				return nil, errInstalledArguments
			}
			command = value
		}
	}
	var args []string
	for command = strings.TrimSpace(command); command != ""; command = strings.TrimSpace(command) {
		value, remainder, err := consumeSystemdArgument(command)
		if err != nil {
			return nil, err
		}
		if strings.Contains(strings.ReplaceAll(value, "%%", ""), "%") {
			return nil, errInstalledArguments
		}
		args = append(args, strings.ReplaceAll(value, "%%", "%"))
		command = remainder
	}
	if len(args) == 0 {
		return nil, errInstalledArguments
	}
	return args, nil
}

func consumeSystemdArgument(command string) (string, string, error) {
	if command[0] != '"' {
		end := strings.IndexAny(command, " \t")
		if end < 0 {
			end = len(command)
		}
		value := command[:end]
		if strings.ContainsAny(value, "\\\"'") {
			return "", "", errInstalledArguments
		}
		return value, command[end:], nil
	}
	end := 1
	for ; end < len(command); end++ {
		if command[end] == '\\' {
			end++
			continue
		}
		if command[end] == '"' {
			break
		}
	}
	if end >= len(command) {
		return "", "", errInstalledArguments
	}
	value, err := strconv.Unquote(command[:end+1])
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", errInstalledArguments, err)
	}
	remainder := command[end+1:]
	if remainder != "" && remainder[0] != ' ' && remainder[0] != '\t' {
		return "", "", errInstalledArguments
	}
	return value, remainder, nil
}
