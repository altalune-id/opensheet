package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startAccountsNATS(t *testing.T) string {
	t.Helper()
	opts := &server.Options{}
	require.NoError(t, opts.ProcessConfigString(fmt.Sprintf(`
listen: "127.0.0.1:-1"
jetstream { store_dir: %q }
accounts {
  APP_A { jetstream: enabled, users: [{ user: app_a, password: pw_a }] }
  APP_B { jetstream: enabled, users: [{ user: app_b, password: pw_b }] }
}
`, t.TempDir())))
	opts.NoLog, opts.NoSigs = true, true
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(ns.Shutdown)
	return ns.ClientURL()
}

func accountOptions(url, user, password string) Options {
	o := testOptions(url)
	o.User, o.Password = user, password
	return o
}

func TestConnect_UserPasswordCreatesStreamsInItsAccount(t *testing.T) {
	c := connectTest(t, accountOptions(startAccountsNATS(t), "app_a", "pw_a"))

	assert.Equal(t, []string{"jobs.>"}, streamInfo(t, c, streamWork).Config.Subjects)
}

func TestConnect_AccountsKeepSameNamedStreamsApart(t *testing.T) {
	url := startAccountsNATS(t)
	a := connectTest(t, accountOptions(url, "app_a", "pw_a"))
	b := connectTest(t, accountOptions(url, "app_b", "pw_b"))
	job := Job{Name: "todo.log_completion", Version: 1}
	require.NoError(t, a.Declare([]Job{job}, nil))
	require.NoError(t, b.Declare([]Job{job}, nil))

	require.NoError(t, a.Submit(t.Context(), job, map[string]string{"from": "a"}))

	assert.Equal(t, uint64(1), streamInfo(t, a, streamWork).State.Msgs)
	assert.Equal(t, uint64(0), streamInfo(t, b, streamWork).State.Msgs)
}

func TestConnect_WrongPasswordFails(t *testing.T) {
	o := accountOptions(startAccountsNATS(t), "app_a", "wrong")
	o.ConnectTimeout = time.Second

	_, err := Connect(t.Context(), o)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Authorization Violation")
}
