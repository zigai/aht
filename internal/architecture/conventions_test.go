package architecture

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/catalog"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestAdapterNamesStayOwned(t *testing.T) {
	t.Parallel()

	owners := make(map[string]string)

	for _, adapter := range catalog.All() {
		id := adapter.Definition().ID
		owners[string(id)] = "internal/harness/" + catalog.DistributionFor(id).Directory + "/"
	}

	visitProductionGo(t, func(path string, data []byte) {
		if path == "pkg/aht/harnesses.go" || strings.HasPrefix(path, "pkg/fakeprovider/launch/") {
			return
		}

		f, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
		if err != nil {
			t.Fatal(err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			literal, ok := n.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}

			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}

			if owner, found := owners[value]; found && !strings.HasPrefix(path, owner) {
				t.Errorf("%s contains adapter name %q owned by %s", path, value, owner)
			}

			return true
		})
	})
}

func TestReporterIdentityHasNoAttributeKeys(t *testing.T) {
	t.Parallel()
	visitProductionGo(t, func(path string, data []byte) {
		if bytes.Contains(data, []byte(`"aht_`)) {
			t.Errorf("%s encodes aht metadata as attribute keys instead of typed fields", path)
		}
	})
}

func TestRenamesLeaveNoDeprecatedAliases(t *testing.T) {
	t.Parallel()
	visitProductionGo(t, func(path string, data []byte) {
		fset := token.NewFileSet()

		f, err := parser.ParseFile(fset, path, data, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}

		for _, group := range f.Comments {
			for line := range strings.Lines(group.Text()) {
				if strings.HasPrefix(line, "Deprecated:") {
					t.Errorf(
						"%s declares a deprecated identifier; rename callers instead of keeping the old name",
						fset.Position(group.Pos()),
					)
				}
			}
		}
	})
}

func TestSnapshotPersistenceCannotObserve(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"Observe", "ObserveBatch"} {
		if _, ok := reflect.TypeFor[*registry.FileStore]().MethodByName(method); ok {
			t.Fatalf("FileStore exposes state mutation %s", method)
		}
	}
}

func TestHarnessPathsResolveHomeThroughOneHelper(t *testing.T) {
	t.Parallel()
	visitProductionGo(t, func(path string, data []byte) {
		if !strings.HasPrefix(path, "internal/harness/") &&
			!strings.HasPrefix(path, "pkg/history/") {
			return
		}

		if path == "internal/harness/location.go" {
			return
		}

		f, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
		if err != nil {
			t.Fatal(err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && looksUpHomeDirectory(call) {
				t.Errorf("%s resolves the home directory itself; use harness.HomeDir", path)
			}

			return true
		})
	})
}

func looksUpHomeDirectory(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := selector.X.(*ast.Ident)
	if !ok || pkg.Name != "os" {
		return false
	}

	if selector.Sel.Name == "UserHomeDir" {
		return true
	}

	return selector.Sel.Name == "Getenv" && len(call.Args) == 1 &&
		isStringLiteral(call.Args[0], "HOME")
}

func isStringLiteral(expr ast.Expr, want string) bool {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}

	value, err := strconv.Unquote(literal.Value)

	return err == nil && value == want
}

func visitProductionGo(t *testing.T, visit func(string, []byte)) {
	t.Helper()
	visitGo(t, func(path string, data []byte) {
		if !strings.HasSuffix(path, "_test.go") {
			visit(path, data)
		}
	})
}

func visitGo(t *testing.T, visit func(string, []byte)) {
	t.Helper()

	rootFS, err := os.OpenRoot(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := rootFS.Close(); err != nil {
			t.Error(err)
		}
	}()

	err = fs.WalkDir(rootFS.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			if path != "." && strings.HasPrefix(entry.Name(), ".") {
				return fs.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		data, err := rootFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read convention source: %w", err)
		}

		visit(filepath.ToSlash(path), data)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSourcesSupportLinuxOrDarwin(t *testing.T) {
	t.Parallel()
	visitGo(t, func(path string, data []byte) {
		if strings.HasSuffix(path, "_other.go") || strings.HasSuffix(path, "_other_test.go") {
			t.Errorf("%s is a platform fallback; use linux or darwin sources", path)
		}

		set := token.NewFileSet()

		file, err := parser.ParseFile(set, path, data, parser.PackageClauseOnly)
		if err != nil {
			t.Fatal(err)
		}

		var expr constraint.Expr

		for line := range strings.Lines(string(data[:set.Position(file.Package).Offset])) {
			if !constraint.IsGoBuild(line) && !constraint.IsPlusBuild(line) {
				continue
			}

			parsed, err := constraint.Parse(line)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}

			if expr == nil {
				expr = parsed
			} else {
				expr = &constraint.AndExpr{X: expr, Y: parsed}
			}
		}

		if expr != nil && !constraintSupports(expr, "linux") &&
			!constraintSupports(expr, "darwin") {
			t.Errorf("%s has a build constraint excluding linux and darwin: %s", path, expr)
		}
	})
}

func constraintSupports(expr constraint.Expr, platform string) bool {
	tags := make(map[string]bool)
	for name := range strings.FieldsSeq("aix android darwin dragonfly freebsd hurd illumos ios js linux " +
		"nacl netbsd openbsd plan9 solaris wasip1 windows zos") {
		tags[name] = name == platform
	}

	tags["unix"] = true

	return constraintMatches(expr, tags)
}

func constraintMatches(expr constraint.Expr, tags map[string]bool) bool {
	unknown := ""

	result := expr.Eval(func(tag string) bool {
		value, found := tags[tag]
		if !found {
			unknown = tag
		}

		return value
	})
	if unknown == "" {
		return result
	}

	tags[unknown] = false
	without := constraintMatches(expr, tags)
	tags[unknown] = true
	with := constraintMatches(expr, tags)
	delete(tags, unknown)

	return without || with
}
