package activities

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// writerMethods is control.Writer's method set: every external write.
func writerMethods() map[string]bool {
	typ := reflect.TypeFor[control.Writer]()
	out := make(map[string]bool, typ.NumMethod())

	for i := range typ.NumMethod() {
		out[typ.Method(i).Name] = true
	}

	return out
}

// calls reports whether n contains a call to a method or function named
// by match.
func calls(n ast.Node, match func(string) bool) bool {
	found := false

	ast.Inspect(n, func(n ast.Node) bool {
		if found {
			return false
		}

		// A function literal is its own scope; its writes are checked in
		// its own blocks.
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}

		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			found = match(fn.Sel.Name)
		case *ast.Ident:
			found = match(fn.Name)
		}

		return !found
	})

	return found
}

// unfencedWrites lists every statement in f that calls a Writer method
// without the statement before it, in the same block, calling fence.
func unfencedWrites(fset *token.FileSet, f *ast.File, methods map[string]bool) []string {
	var out []string

	isWrite := func(name string) bool { return methods[name] }
	isFence := func(name string) bool { return name == "fence" }

	ast.Inspect(f, func(n ast.Node) bool {
		block, ok := n.(*ast.BlockStmt)
		if !ok {
			return true
		}

		for i, stmt := range block.List {
			if !calls(stmt, isWrite) || stmtIsBlockOwner(stmt) {
				continue
			}

			if i == 0 || !calls(block.List[i-1], isFence) {
				out = append(out, fset.Position(stmt.Pos()).String())
			}
		}

		return true
	})

	return out
}

// stmtIsBlockOwner reports whether stmt holds the write only inside its
// nested blocks (an if, for, switch or select body), which are checked
// on their own. A write in an if's init or condition is the statement's.
func stmtIsBlockOwner(stmt ast.Stmt) bool {
	writes := writerMethods()
	isWrite := func(name string) bool { return writes[name] }

	switch s := stmt.(type) {
	case *ast.IfStmt:
		return (s.Init == nil || !calls(s.Init, isWrite)) && !calls(s.Cond, isWrite)
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.BlockStmt:
		return true
	default:
		return false
	}
}

// TestUnfencedWrites_CatchesAnUnfencedWrite proves the walk below is not
// vacuous: an unfenced write is found and a fenced one is not.
func TestUnfencedWrites_CatchesAnUnfencedWrite(t *testing.T) {
	t.Parallel()

	const src = `package p

func fenced(ctx context.Context, run *remediationRun, w control.Writer) error {
	if err := run.fence(ctx); err != nil {
		return err
	}
	if err := w.CreateRef(ctx, "b", "sha"); err != nil {
		return err
	}
	return nil
}

func unfenced(ctx context.Context, w control.Writer) error {
	return w.ClosePullRequest(ctx, 1)
}

func late(ctx context.Context, run *remediationRun, w control.Writer) error {
	if err := run.fence(ctx); err != nil {
		return err
	}
	x := 1
	_ = x
	return w.UpsertLabel(ctx, control.Label{})
}
`

	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, "probe.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	got := unfencedWrites(fset, f, writerMethods())
	if len(got) != 2 || !strings.HasPrefix(got[0], "probe.go:14") || !strings.HasPrefix(got[1], "probe.go:23") {
		t.Errorf("unfencedWrites = %v, want lines 14 and 23", got)
	}
}

// TestRemediate_EveryWriteIsFenced is IMPL-0030 3.5: every external write
// in the remediation activities is immediately preceded by the epoch
// fence.
func TestRemediate_EveryWriteIsFenced(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("remediate*.go")
	if err != nil {
		t.Fatal(err)
	}

	methods := writerMethods()
	fset := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		for _, pos := range unfencedWrites(fset, f, methods) {
			t.Errorf("%s: a Writer call not immediately preceded by run.fence", pos)
		}
	}
}

// TestFence is IMPL-0030 3.5: the fence passes while the assignment is
// unchanged and returns ErrStaleEpoch when any guarded field moved or
// the control was withdrawn.
func TestFence(t *testing.T) {
	t.Parallel()

	for name, move := range map[string]func(*store.RemediationTarget){
		"epoch":          func(t *store.RemediationTarget) { t.Epoch++ },
		"effective mode": func(t *store.RemediationTarget) { t.EffectiveMode = "evaluate" },
		"revision":       func(t *store.RemediationTarget) { t.Revision = "r2" },
		"state":          func(t *store.RemediationTarget) { t.State = "excluded" },
		"remediable":     func(t *store.RemediationTarget) { t.Remediable = false },
		"parked":         func(t *store.RemediationTarget) { t.Active = false },
	} {
		moved := remediable()
		move(moved)

		run := &remediationRun{
			in: remediateInput(), target: remediable(),
			store: &fakeRemediationStore{targets: []*store.RemediationTarget{remediable(), moved}},
		}

		if err := run.fence(context.Background()); err != nil {
			t.Fatalf("%s: first fence = %v, want nil", name, err)
		}

		if err := run.fence(context.Background()); !errors.Is(err, ErrStaleEpoch) {
			t.Errorf("%s: fence after the move = %v, want ErrStaleEpoch", name, err)
		}
	}

	// A new evaluation (generation, intent) does not stale the run.
	newer := remediable()
	newer.Generation, newer.IntentRevision = 6, "i6"

	run := &remediationRun{in: remediateInput(), target: remediable(), store: &fakeRemediationStore{targets: []*store.RemediationTarget{newer}}}
	if err := run.fence(context.Background()); err != nil {
		t.Errorf("fence after a new evaluation = %v, want nil", err)
	}

	gone := &remediationRun{in: remediateInput(), target: remediable(), store: &fakeRemediationStore{targets: []*store.RemediationTarget{nil}}}
	if err := gone.fence(context.Background()); !errors.Is(err, ErrStaleEpoch) {
		t.Errorf("fence after withdrawal = %v, want ErrStaleEpoch", err)
	}
}
