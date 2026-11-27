package pathmatch_test

import (
	"strings"
	"testing"

	"github.com/zigai/aht/v2/pkg/pathmatch"
)

func TestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"", "/work/example-project", false},
		{"/work/example-project", "", false},
		{"", "", false},

		{"/work/example-project", "/work/example-project", true},
		{"/work/example-project/", "/work/example-project", true},
		{"/work/example-project", "/work/example-project/", true},
		{"/work/example-project", "/work/example", false},
		{"work/example-project", "/work/example-project", false},
		{"/work/example-project", "work/example-project", false},
		{"work/example-project", "work/example-project/src", true},

		{"/srv/u/clients", "/srv/u/clients/app", true},
		{"/srv/u/clients", "/srv/u/clients/app/deep", true},
		{"/srv/u/clients", "/srv/u/clients2", false},
		{"/srv/u/clients", "/srv/u/clients-old", false},
		{"/srv/u/clients", "/srv/u", false},

		{"/tmp/*", "/tmp", true},
		{"/tmp/*", "/tmp/a", true},
		{"/tmp/*", "/tmp/a/b", true},
		{"/tmp/*", "/tmpdir", false},
		{"/tmp/*", "/var/tmp/a", false},
		{"/home/bob/*", "/home/bob/service", true},
		{"/home/bob/*", "/home/alice/service", false},
		{"/*", "/anything/below", true},
		{"/", "/anything/below", true},
		{"/", "/", true},
		{"/", "relative/path", false},
		{"/tmp", "/", false},

		{"/srv/u/clients-*", "/srv/u/clients-old", true},
		{"/srv/u/clients-*", "/srv/u/clients-old/app", true},
		{"/srv/u/clients-*", "/srv/u/clients-old/app/deep", true},
		{"/srv/u/clients-*", "/srv/u/clients", false},
		{"/srv/u/clients-*", "/srv/u/other", false},
		{"/srv/u/clients-*", "/srv/u/other/clients-old", false},
		{"/srv/u/*/app", "/srv/u/one/app/src", true},
		{"/srv/u/*/app", "/srv/u/one/two/app", false},
		{"/srv/u/client?", "/srv/u/clientA/app", true},
		{"/srv/u/[ab]", "/srv/u/b/app", true},
		{"/srv/u/[ab]", "/srv/u/c/app", false},

		{"**/private/**", "/work/private", true},
		{"**/private/**", "/work/private/project", true},
		{"**/private/**", "/private", true},
		{"**/private/**", "/a/b/c/private/d/e", true},
		{"**/private/**", "private/x", true},
		{"**/private/**", "/work/privateer", false},
		{"**/private/**", "/work/not-private/x", false},
		{"**/node_modules", "/repo/node_modules", true},
		{"**/node_modules", "/repo/node_modules/pkg/lib", true},
		{"**/node_modules", "/node_modules", true},
		{"**/node_modules", "/repo/pkg/node_modules", true},
		{"**/node_modules", "/repo/node_modules_old", false},
		{"**/node_modules/**", "/repo/node_modules", true},
		{"**/node_modules/**", "/repo/a/node_modules/b/c", true},

		{"/srv/u/**/build", "/srv/u/build", true},
		{"/srv/u/**/build", "/srv/u/work/build", true},
		{"/srv/u/**/build", "/srv/u/work/a/b/build", true},
		{"/srv/u/**/build", "/srv/u/work/a/b/build/nested", true},
		{"/srv/u/**/build", "/srv/u/work/builder", false},
		{"/srv/u/**/build", "/srv/u/work", false},
		{"/srv/u/**/build", "/other/work/build", false},
		{"/srv/u/**/build", "/srv/ux/build", false},

		{"/srv/u/**", "/srv/u", true},
		{"/srv/u/**", "/srv/u/a", true},
		{"/srv/u/**", "/srv/u/a/b", true},
		{"/srv/u/**", "/srv/ux", false},
		{"/srv/u/**", "/srv", false},
		{"**", "/anything", true},
		{"**", "relative", true},

		{"**/a/**/b", "/a/b", true},
		{"**/a/**/b", "/x/a/y/z/b", true},
		{"**/a/**/b", "/x/a/y/z/b/c", true},
		{"**/a/**/b", "/x/b/y/a", false},
		{"/**/**/x", "/x", true},
		{"/**/**/x", "/p/q/x", true},
		{"/**/**/x", "/p/q/y", false},

		{"*/foo", "/foo", false},
		{"*", "/foo", false},
		{"*", "foo/bar", true},

		{"[", "/srv/a", false},
		{"/srv/[", "/srv/a", false},
		{"/srv/[", "/srv/[/child", true},
		{"/srv/a[", "/srv/a", false},
		{"**/[", "/srv/a", false},
	}

	for _, test := range tests {
		if got := pathmatch.Match(test.path, test.pattern); got != test.want {
			t.Errorf("Match(%q, %q) = %v, want %v", test.path, test.pattern, got, test.want)
		}
	}
}

func TestMatchRepeatedDoubleStarDoesNotBacktrackExponentially(t *testing.T) {
	t.Parallel()

	pattern := strings.Repeat("**/a/", 40) + "b"
	path := "/" + strings.Repeat("a/", 200) + "c"

	if pathmatch.Match(path, pattern) {
		t.Fatalf("Match(%q, %q) = true, want false", path, pattern)
	}
}

func TestMatchAny(t *testing.T) {
	t.Parallel()

	tests := []struct {
		patterns []string
		paths    []string
		want     bool
	}{
		{nil, []string{"/work/a"}, false},
		{[]string{"/work/a"}, nil, false},
		{[]string{"/work/b", "/work/a"}, []string{"/work/a/src"}, true},
		{[]string{"/work/b"}, []string{"", "/work/b"}, true},
		{[]string{"/work/b"}, []string{"/work/a", "/work/c"}, false},
	}

	for _, test := range tests {
		if got := pathmatch.MatchAny(test.patterns, test.paths...); got != test.want {
			t.Errorf("MatchAny(%q, %q) = %v, want %v", test.patterns, test.paths, got, test.want)
		}
	}
}
