package architecture

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
		if path == "pkg/aht/harnesses.go" {
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

func TestSnapshotPersistenceCannotObserve(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"Observe", "ObserveBatch"} {
		if _, ok := reflect.TypeFor[*registry.FileStore]().MethodByName(method); ok {
			t.Fatalf("FileStore exposes state mutation %s", method)
		}
	}
}

func visitProductionGo(t *testing.T, visit func(string, []byte)) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	rootFS, err := os.OpenRoot(root)
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
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
