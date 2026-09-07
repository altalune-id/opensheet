package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletion_Bash(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"completion", "bash"})
	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.NotEmpty(t, buf.String())
}

func TestCompletion_Zsh(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"completion", "zsh"})
	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.NotEmpty(t, buf.String())
}

func TestCompletion_Fish(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"completion", "fish"})
	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.NotEmpty(t, buf.String())
}

func TestCompletion_Powershell(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"completion", "powershell"})
	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.NotEmpty(t, buf.String())
}

func TestCompletion_UnknownShellRejected(t *testing.T) {
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"completion", "tcsh"})
	err := root.ExecuteContext(context.Background())
	require.Error(t, err)
}

func TestVersion_JSON(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--output", "json", "version"})
	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.Contains(t, buf.String(), "version")
}

func TestVersion_NDJSON(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--output", "ndjson", "version"})
	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.NotEmpty(t, strings.TrimSpace(buf.String()))
}

func TestOrgCreate_MissingRequiredFlags(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"org", "create"})
	err := root.ExecuteContext(context.Background())
	require.Error(t, err)
}

func TestProjectCreate_MissingRequiredFlags(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"project", "create"})
	err := root.ExecuteContext(context.Background())
	require.Error(t, err)
}

func TestInviteSend_MissingRequiredFlags(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"invite", "send"})
	err := root.ExecuteContext(context.Background())
	require.Error(t, err)
}
