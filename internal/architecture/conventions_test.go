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
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

const modulePath = "github.com/zigai/aht/v2"

var internalAttributeKeyPatterns = []*regexp.Regexp{
	regexp.MustCompile("[`\"']aht_[a-z0-9_]+[ \\t]*="),
	regexp.MustCompile(`--attribute[^A-Za-z0-9_]{0,8}aht_[a-z0-9_]+`),
}

func TestAdapterNamesStayOwned(t *testing.T) {
	t.Parallel()

	owners := make(map[string]string)

	for _, adapter := range adapters.All() {
		id := adapter.Definition().ID
		owners[string(id)] = "internal/harness/" + adapters.DistributionFor(id).Directory + "/"
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

	visitHarnessAssets(t, func(path string, data []byte) {
		if match := internalAttributeKey(string(data)); match != "" {
			t.Errorf("%s emits %q as a report attribute; use native attribute names", path, match)
		}
	})
}

func TestPublicPackagesDoNotExposeInternalTypes(t *testing.T) {
	t.Parallel()

	visitProductionGo(t, func(path string, data []byte) {
		if !strings.HasPrefix(path, "pkg/") {
			return
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
		if err != nil {
			t.Fatal(err)
		}

		internal := internalImportNames(file)
		if len(internal) == 0 {
			return
		}

		surface := publicSurface{t: t, file: path, internal: internal}

		for _, decl := range file.Decls {
			surface.checkDecl(decl)
		}
	})
}

func internalAttributeKey(text string) string {
	for _, pattern := range internalAttributeKeyPatterns {
		if match := pattern.FindString(text); match != "" {
			return match
		}
	}

	return ""
}

func internalImportNames(file *ast.File) map[string]bool {
	names := make(map[string]bool)

	for _, spec := range file.Imports {
		name, ok := internalImportName(spec)
		if ok {
			names[name] = true
		}
	}

	return names
}

func internalImportName(spec *ast.ImportSpec) (string, bool) {
	imported, err := strconv.Unquote(spec.Path.Value)
	if err != nil || !strings.HasPrefix(imported, modulePath+"/internal/") {
		return "", false
	}

	name := filepath.Base(imported)
	if spec.Name != nil {
		name = spec.Name.Name
	}

	if name == "_" || name == "." {
		return "", false
	}

	return name, true
}

type publicSurface struct {
	t        *testing.T
	file     string
	internal map[string]bool
}

func (s publicSurface) checkDecl(decl ast.Decl) {
	switch node := decl.(type) {
	case *ast.FuncDecl:
		s.checkFunc(node)
	case *ast.GenDecl:
		for _, spec := range node.Specs {
			s.checkSpec(spec)
		}
	}
}

func (s publicSurface) checkSpec(spec ast.Spec) {
	switch node := spec.(type) {
	case *ast.TypeSpec:
		s.checkTypeSpec(node)
	case *ast.ValueSpec:
		s.checkValueSpec(node)
	}
}

func (s publicSurface) checkTypeSpec(spec *ast.TypeSpec) {
	if !spec.Name.IsExported() {
		return
	}

	s.checkConstraints(spec.Name.Name, spec.TypeParams)
	s.checkExpr(spec.Name.Name, spec.Type)
}

func (s publicSurface) checkValueSpec(spec *ast.ValueSpec) {
	for _, name := range spec.Names {
		if name.IsExported() {
			s.checkExpr(name.Name, spec.Type)
		}
	}
}

func (s publicSurface) checkFunc(decl *ast.FuncDecl) {
	symbol, ok := publicFuncSymbol(decl)
	if !ok {
		return
	}

	s.checkConstraints(symbol, decl.Type.TypeParams)
	s.checkFieldList(symbol, decl.Type.Params)
	s.checkFieldList(symbol, decl.Type.Results)
}

func (s publicSurface) checkConstraints(symbol string, params *ast.FieldList) {
	s.checkFieldList(symbol, params)
}

func (s publicSurface) checkFieldList(symbol string, fields *ast.FieldList) {
	if fields == nil {
		return
	}

	for _, field := range fields.List {
		s.checkExpr(symbol, field.Type)
	}
}

func (s publicSurface) checkExpr(symbol string, expr ast.Expr) {
	if expr == nil {
		return
	}

	ast.Inspect(expr, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.SelectorExpr:
			s.report(symbol, typed)
			return false
		case *ast.StructType:
			s.checkStructFields(symbol, typed)
			return false
		case *ast.InterfaceType:
			s.checkInterface(symbol, typed)
			return false
		}

		return true
	})
}

func (s publicSurface) checkStructFields(symbol string, structType *ast.StructType) {
	for _, field := range structType.Fields.List {
		if !fieldExported(field) {
			continue
		}

		s.checkExpr(symbol, field.Type)
	}
}

func (s publicSurface) checkInterface(symbol string, interfaceType *ast.InterfaceType) {
	for _, method := range interfaceType.Methods.List {
		signature, ok := method.Type.(*ast.FuncType)
		if !ok {
			s.checkExpr(symbol, method.Type)
			continue
		}

		s.checkConstraints(symbol, signature.TypeParams)
		s.checkFieldList(symbol, signature.Params)
		s.checkFieldList(symbol, signature.Results)
	}
}

func (s publicSurface) report(symbol string, selector *ast.SelectorExpr) {
	alias, ok := selector.X.(*ast.Ident)
	if !ok || !s.internal[alias.Name] {
		return
	}

	s.t.Errorf(
		"%s: exported %s exposes internal type %s.%s; external modules cannot name it",
		s.file,
		symbol,
		alias.Name,
		selector.Sel.Name,
	)
}

func publicFuncSymbol(decl *ast.FuncDecl) (string, bool) {
	if decl.Recv == nil {
		return decl.Name.Name, decl.Name.IsExported()
	}

	receiver := receiverTypeName(decl.Recv)
	if receiver == "" || !ast.IsExported(receiver) || !decl.Name.IsExported() {
		return "", false
	}

	return receiver + "." + decl.Name.Name, true
}

func receiverTypeName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}

	expr := ast.Unparen(recv.List[0].Type)

	if star, ok := expr.(*ast.StarExpr); ok {
		expr = ast.Unparen(star.X)
	}

	ident, ok := ast.Unparen(genericBase(expr)).(*ast.Ident)
	if !ok {
		return ""
	}

	return ident.Name
}

func genericBase(expr ast.Expr) ast.Expr {
	index, ok := expr.(*ast.IndexExpr)
	if ok {
		return index.X
	}

	list, ok := expr.(*ast.IndexListExpr)
	if ok {
		return list.X
	}

	return expr
}

func fieldExported(field *ast.Field) bool {
	if len(field.Names) == 0 {
		return true
	}

	for _, name := range field.Names {
		if name.IsExported() {
			return true
		}
	}

	return false
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

	visitRepository(t, func(path string) bool {
		return strings.HasSuffix(path, ".go")
	}, visit)
}

func visitHarnessAssets(t *testing.T, visit func(string, []byte)) {
	t.Helper()

	visitRepository(t, isHarnessAsset, visit)
}

func isHarnessAsset(path string) bool {
	if !strings.HasPrefix(path, "internal/harness/") || !strings.Contains(path, "/assets/") {
		return false
	}

	switch filepath.Ext(path) {
	case ".tmpl", ".ts", ".js", ".py", ".sh":
		return true
	default:
		return false
	}
}

func visitRepository(t *testing.T, include func(string) bool, visit func(string, []byte)) {
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

		slashed := filepath.ToSlash(path)
		if !include(slashed) {
			return nil
		}

		data, err := rootFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read convention source: %w", err)
		}

		visit(slashed, data)

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
