package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/cli"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/scheduler"
)

func schedulerBootStub(t *testing.T) cli.ServerBootFn {
	t.Helper()
	runner, err := scheduler.New(scheduler.Options{Logger: slog.Default()})
	require.NoError(t, err)
	require.NoError(t, runner.Register(scheduler.Job{
		Name:      "system-sweep",
		Scope:     scheduler.ScopeSystem,
		Schedule:  scheduler.MustEveryInterval(time.Minute, 0),
		Timeout:   5 * time.Second,
		Singleton: false,
		Run:       func(context.Context) error { return nil },
	}))
	return func(_ context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		return &boot.Server{Cfg: cfg, Scheduler: runner}, nil
	}
}

func TestSchedulerList_Text(t *testing.T) {
	buf := new(bytes.Buffer)
	root := cli.NewRootCmd(schedulerBootStub(t), nil)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"scheduler", "list"})

	require.NoError(t, root.ExecuteContext(t.Context()))
	out := buf.String()
	require.Contains(t, out, "system-sweep")
	require.Contains(t, out, "system")
}

func TestSchedulerList_JSON(t *testing.T) {
	buf := new(bytes.Buffer)
	root := cli.NewRootCmd(schedulerBootStub(t), nil)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"scheduler", "list", "--output", "json"})

	require.NoError(t, root.ExecuteContext(t.Context()))

	var envelope struct {
		Data []struct {
			Name      string `json:"name"`
			Scope     string `json:"scope"`
			Singleton bool   `json:"singleton"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &envelope))
	require.NotEmpty(t, envelope.Data)
	require.Equal(t, "system-sweep", envelope.Data[0].Name)
}

func TestSchedulerRun_RequiresExactlyOneArg(t *testing.T) {
	for _, args := range [][]string{{"scheduler", "run"}, {"scheduler", "run", "a", "b"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := cli.NewRootCmd(schedulerBootStub(t), nil)
			root.SetOut(new(bytes.Buffer))
			root.SetErr(new(bytes.Buffer))
			root.SetArgs(args)
			require.Error(t, root.ExecuteContext(t.Context()))
		})
	}
}

func TestSchedulerRun_UnknownJobExitsNotFound(t *testing.T) {
	root := cli.NewRootCmd(schedulerBootStub(t), nil)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"scheduler", "run", "no-such-job"})

	err := root.ExecuteContext(t.Context())
	require.Error(t, err)
	require.Equal(t, cli.ExitNotFound, cli.ExitCodeFor(err))
}

func TestServe_SchedulerFlagsAreMutuallyExclusive(t *testing.T) {
	root := cli.NewRootCmd(schedulerBootStub(t), nil)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"serve", "--no-scheduler", "--scheduler-only"})

	err := root.ExecuteContext(t.Context())
	require.ErrorContains(t, err, "none of the others can be")
}
