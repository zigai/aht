package transcript

import "strings"

func SplitPrompt(body string) (string, string) {
	body = strings.TrimSpace(body)

	text := body
	for {
		_, _, rest, ok := LeadingBlock(text)
		if !ok {
			break
		}

		text = strings.TrimSpace(rest)
	}

	return strings.TrimSpace(body[:len(body)-len(text)]), text
}

func UserParts(injected string, typed string) []TextPart {
	var parts []TextPart
	if injected != "" {
		parts = append(parts, TextPart{Role: InjectedUserRole, Text: injected})
	}

	if typed != "" {
		parts = append(parts, TextPart{Role: TypedUserRole, Text: typed})
	}

	return parts
}

func LeadingBlock(text string) (string, string, string, bool) {
	if !strings.HasPrefix(text, "<") {
		return "", "", "", false
	}

	end := 1
	for end < len(text) && blockNameByte(text[end]) {
		end++
	}

	if end == 1 || end == len(text) {
		return "", "", "", false
	}

	name := text[1:end]

	open := strings.IndexByte(text, '>')
	if open < 0 || !blockNameTerminator(text[end]) {
		return "", "", "", false
	}

	if text[open-1] == '/' {
		return name, "", text[open+1:], true
	}

	closing := "</" + name + ">"

	content, rest, ok := strings.Cut(text[open+1:], closing)
	if !ok {
		return "", "", "", false
	}

	return name, content, rest, true
}

func blockNameTerminator(character byte) bool {
	return character == '>' || character == ' ' || character == '/' || character == '\n'
}

func blockNameByte(character byte) bool {
	return character == '-' || character == '_' || character == ':' ||
		character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}
