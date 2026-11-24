package launch

import "strings"

// TitleRequest matches a native title prompt by role and its outer text, or
// a native title tool declaration. Prompt text in other roles is ignored.
//
//exhaustruct:optional
type TitleRequest struct {
	Role     string
	Prefix   string
	Contains string
	Suffix   string
	Tool     string
}

func (plan Plan) TitleRequest(value map[string]any) bool {
	for _, rule := range plan.TitleRequests {
		if rule.Tool != "" {
			if hasRequestTool(value, rule.Tool) {
				return true
			}

			continue
		}

		for _, text := range requestPrompts(value, rule.Role) {
			text = strings.TrimSpace(text)
			if strings.HasPrefix(text, rule.Prefix) && strings.Contains(text, rule.Contains) &&
				strings.HasSuffix(text, rule.Suffix) {
				return true
			}
		}
	}

	return false
}

func hasRequestTool(value map[string]any, name string) bool {
	for _, item := range requestObjects(value["tools"]) {
		function, _ := item["function"].(map[string]any)
		if item["name"] == name || function["name"] == name {
			return true
		}

		for _, decl := range requestObjects(item["functionDeclarations"]) {
			if decl["name"] == name {
				return true
			}
		}
	}

	return false
}

func requestPrompts(value map[string]any, role string) []string {
	var prompts []string

	if role == "system" {
		for _, field := range []string{"system", "instructions"} {
			if text := requestText(value[field]); text != "" {
				prompts = append(prompts, text)
			}
		}

		if instruction, ok := value["systemInstruction"].(map[string]any); ok {
			if text := requestText(instruction["parts"]); text != "" {
				prompts = append(prompts, text)
			}
		}
	}

	if role == "user" {
		if text, ok := value["input"].(string); ok {
			prompts = append(prompts, text)
		}
	}

	for _, field := range []string{"messages", "input"} {
		prompts = appendRolePrompts(prompts, requestObjects(value[field]), role, "content")
	}

	return appendRolePrompts(prompts, requestObjects(value["contents"]), role, "parts")
}

func appendRolePrompts(
	prompts []string,
	items []map[string]any,
	role string,
	contentKey string,
) []string {
	for _, item := range items {
		if item["role"] != role {
			continue
		}

		text := requestText(item[contentKey])
		if role == "user" {
			prompts = []string{text}
		} else {
			prompts = append(prompts, text)
		}
	}

	return prompts
}

func requestText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}

	var parts []string

	for _, item := range requestObjects(value) {
		if text, ok := item["text"].(string); ok {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n")
}

func requestObjects(value any) []map[string]any {
	items, _ := value.([]any)

	var objects []map[string]any

	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			objects = append(objects, object)
		}
	}

	return objects
}
