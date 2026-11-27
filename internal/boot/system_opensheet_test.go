package boot

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
	"altalune.id/opensheet/scheduler"
)

func canAll(ctx context.Context) error {
	ctx = tenant.Into(ctx, tenant.Context{OrgID: uuid.New()})
	for _, d := range authz.Catalogue() {
		if err := authz.Can(ctx, d.Permission); err != nil {
			return err
		}
	}
	return nil
}

func TestSystemWrappers_HoldEveryPermission(t *testing.T) {
	require.Error(t, canAll(t.Context()), "an unwrapped context holds nothing")

	var seen []error
	record := func(ctx context.Context) error { seen = append(seen, canAll(ctx)); return nil }

	require.NoError(t, systemJob(scheduler.Job{Run: record}).Run(t.Context()))
	h := systemHandler(queue.Handler{
		Handle:       func(ctx context.Context, _ queue.Message) error { return record(ctx) },
		OnDeadLetter: func(ctx context.Context, _ queue.DeadLetter) error { return record(ctx) },
	})
	require.NoError(t, h.Handle(t.Context(), queue.Message{}))
	require.NoError(t, h.OnDeadLetter(t.Context(), queue.DeadLetter{}))
	l := systemListener(queue.Listener{Handle: func(ctx context.Context, _ queue.Message) error { return record(ctx) }})
	require.NoError(t, l.Handle(t.Context(), queue.Message{}))

	require.Equal(t, []error{nil, nil, nil, nil}, seen)
	require.Nil(t, systemHandler(queue.Handler{}).OnDeadLetter, "a missing dead-letter hook stays missing")
}

func TestSweepWriteAttempts_RunsAsSystem(t *testing.T) {
	srv, err := BootServer(context.Background(), schedulerBootCfg(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	var names []string
	for _, j := range srv.Scheduler.Jobs() {
		names = append(names, j.Name)
		fn := runtime.FuncForPC(reflect.ValueOf(j.Run).Pointer()).Name()
		require.Contains(t, fn, ".systemJob.func", "job %q must run under authz.AsSystem", j.Name)
	}
	require.Contains(t, names, "sheet-write-attempt-sweep")
}

func TestConsumersAndListeners_RunAsSystem(t *testing.T) {
	var seen []error
	handle := func(ctx context.Context, _ queue.Message) error { seen = append(seen, canAll(ctx)); return nil }

	hs := handlersOf([]queue.Provider{handlerProvider{{Handle: handle}}})
	require.Len(t, hs, 1)
	require.NoError(t, hs[0].Handle(t.Context(), queue.Message{}))

	ls := listenersOf([]queue.ListenerProvider{listenerProvider{{Handle: handle}}})
	require.Len(t, ls, 1)
	require.NoError(t, ls[0].Handle(t.Context(), queue.Message{}))

	require.Equal(t, []error{nil, nil}, seen)
}

type handlerProvider []queue.Handler

func (p handlerProvider) ConsumerHandlers() []queue.Handler { return p }

type listenerProvider []queue.Listener

func (p listenerProvider) Listeners() []queue.Listener { return p }

const authzImport = "altalune.id/opensheet/internal/platform/authz"

// TestAsSystem_OnlyInBootWrappers scans every non-test Go file. SECURITY: AsSystem holds every permission, so only the background-work wrappers may call it.
func TestAsSystem_OnlyInBootWrappers(t *testing.T) {
	fset := token.NewFileSet()
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if path == filepath.Join(root, "internal", "boot", "system_opensheet.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		if callsAsSystem(f) {
			t.Errorf("%s calls authz.AsSystem; only boot's background-work wrappers may", path)
		}
		return nil
	})
	require.NoError(t, err)
}

func callsAsSystem(f *ast.File) bool {
	name := ""
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != authzImport {
			continue
		}
		name = "authz"
		if imp.Name != nil {
			name = imp.Name.Name
		}
	}
	if name == "" {
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == name && sel.Sel.Name == "AsSystem" {
				found = true
			}
		}
		return !found
	})
	return found
}

func TestSweepWriteAttempts_RunsForEveryTenantUnderTheGates(t *testing.T) {
	srv, err := BootServer(context.Background(), schedulerBootCfg(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	owner, err := srv.Users.Create(t.Context(), user.CreateRequest{Email: "sweep@example.com", Name: "Sweep", Source: user.SourceOIDC})
	require.NoError(t, err)
	_, err = srv.Orgs.BootstrapSingleton(t.Context(), "sweep-org", "Sweep Org", owner.ID)
	require.NoError(t, err)

	require.NoError(t, srv.Scheduler.RunOnce(t.Context(), "sheet-write-attempt-sweep"))
}
