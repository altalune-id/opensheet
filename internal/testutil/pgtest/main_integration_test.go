//go:build integration

package pgtest

import (
	"os"
	osexec "os/exec"
	"regexp"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

const (
	envChild         = "OPENSHEET_PGTEST_CHILD"
	childWithoutMain = "without-main"
	childContainer   = "container"
)

func TestMain(m *testing.M) {
	if os.Getenv(envChild) == childWithoutMain {
		os.Exit(m.Run())
	}
	Main(m)
}

func TestChild(t *testing.T) {
	if os.Getenv(envChild) == "" {
		t.Skip("runs only as a child of the tests below")
	}
	New(t)
	t.Logf("container=%s", server.ctr.GetContainerID())
}

func runChild(t *testing.T, mode string) ([]byte, error) {
	t.Helper()
	cmd := osexec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestChild$", "-test.v")
	cmd.Env = append(os.Environ(), envChild+"="+mode, envDSN+"=")
	return cmd.CombinedOutput()
}

func TestNew_FailsWhenTestMainSkipsMain(t *testing.T) {
	out, err := runChild(t, childWithoutMain)
	require.Error(t, err, "New must fail without Main:\n%s", out)
	assert.Contains(t, string(out), "pgtest.Main")
}

func TestMain_RemovesTheContainer(t *testing.T) {
	out, err := runChild(t, childContainer)
	require.NoError(t, err, "%s", out)
	match := regexp.MustCompile(`container=(\w+)`).FindSubmatch(out)
	if match == nil {
		t.Skipf("child started no container:\n%s", out)
	}
	provider, err := testcontainers.NewDockerProvider()
	require.NoError(t, err)
	defer func() { _ = provider.Close() }()
	found, err := provider.Client().ContainerList(t.Context(), client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("id", string(match[1])),
	})
	require.NoError(t, err)
	assert.Empty(t, found.Items, "container %s outlived its test binary", match[1])
}
