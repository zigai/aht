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
			for _, item := range requestObjects(value["tools"]) {
				function, _ := item["function"].(map[string]any)
				if item["name"] == rule.Tool || function["name"] == rule.Tool {
					return true
				}
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

func requestPrompts(value map[string]any, role string) []string {
	var prompts []string

	if role == "system" {
		for _, field := range []string{"system", "instructions"} {
			if text := requestText(value[field]); text != "" {
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
		for _, item := range requestObjects(value[field]) {
			if item["role"] == role {
				text := requestText(item["content"])
				if role == "user" {
					prompts = []string{text}
				} else {
					prompts = append(prompts, text)
				}
			}
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
