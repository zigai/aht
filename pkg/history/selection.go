package history

import (
	"strings"
)

var filterRules = []filterRule{
	{
		accepts: Filter.acceptsDirs,
		sql:     func(f Filter) (string, []any) { return directorySQL(f.Dirs) },
	},
	{
		accepts: func(f Filter, c Conversation) bool { return f.acceptsTimes(c) },
		sql: func(f Filter) (string, []any) {
			predicate := "1"

			var args []any

			if !f.Since.IsZero() {
				predicate += " AND c.active>=?"

				args = append(args, f.Since.Unix())
			}

			if !f.Until.IsZero() {
				predicate += " AND c.started<=?"

				args = append(args, f.Until.Unix())
			}

			return predicate, args
		},
	},
	{
		accepts: func(f Filter, c Conversation) bool { return c.Messages >= f.MinMessages },
		sql: func(f Filter) (string, []any) {
			if f.MinMessages > 0 {
				return "c.messages>=?", []any{f.MinMessages}
			}

			return "1", nil
		},
	},
	{
		accepts: func(f Filter, c Conversation) bool { return f.GitBranch == "" || c.GitBranch == f.GitBranch },
		sql: func(f Filter) (string, []any) {
			if f.GitBranch != "" {
				return "c.branch=?", []any{f.GitBranch}
			}

			return "1", nil
		},
	},
	{accepts: func(f Filter, c Conversation) bool {
		return f.Model == "" || strings.Contains(strings.ToLower(c.Model), strings.ToLower(f.Model))
	}, sql: nil},
}

var roleRules = []roleRule{
	{role: RoleUser, visible: true, indexed: true},
	{role: RoleAssistant, visible: true, indexed: true},
	{role: RoleContext, visible: false, indexed: true},
	{role: RoleTool, visible: true, indexed: false},
}

type filterRule struct {
	accepts func(Filter, Conversation) bool
	sql     func(Filter) (string, []any)
}

type roleRule struct {
	role    Role
	visible bool
	indexed bool
}

func (f Filter) accepts(c Conversation) bool {
	for _, rule := range filterRules {
		if !rule.accepts(f, c) {
			return false
		}
	}

	return true
}

func (f Filter) selectionSQL() (string, []any) {
	var predicate strings.Builder
	predicate.WriteString("1")

	var args []any

	for _, rule := range filterRules {
		if rule.sql == nil {
			continue
		}

		fragment, values := rule.sql(f)
		if fragment != "1" {
			predicate.WriteString(" AND (" + fragment + ")")

			args = append(args, values...)
		}
	}

	return predicate.String(), args
}

func (q Query) acceptsRole(role Role) bool {
	if role == RoleTool && !q.IncludeTools {
		return false
	}

	if q.Role != "" {
		return q.Role == RoleAll || q.Role == role
	}

	for _, rule := range roleRules {
		if rule.role == role {
			return rule.visible
		}
	}

	return true
}

func indexedRole(role Role) bool {
	for _, rule := range roleRules {
		if role == rule.role {
			return rule.indexed
		}
	}

	return true
}

func (q Query) rolePredicate(filter string, args []any) (string, []any) {
	if q.Role != "" && q.Role != RoleAll {
		return "(" + filter + ") AND p.role=?", append(args, string(q.Role))
	}

	for _, rule := range roleRules {
		if q.Role == "" && !rule.visible || rule.role == RoleTool && !q.IncludeTools {
			filter = "(" + filter + ") AND p.role<>?"

			args = append(args, string(rule.role))
		}
	}

	return filter, args
}
