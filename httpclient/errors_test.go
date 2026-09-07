package httpclient_test

import (
	"errors"
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/httpclient"
)

func TestIsPrivateAddressError(t *testing.T) {
	err := &httpclient.PrivateAddressError{Address: "169.254.169.254:80", Addr: netip.MustParseAddr("169.254.169.254")}
	require.True(t, httpclient.IsPrivateAddressError(err))
	require.True(t, httpclient.IsPrivateAddressError(fmt.Errorf("dial: %w", err)))
	require.False(t, httpclient.IsPrivateAddressError(errors.New("other")))
	require.False(t, httpclient.IsPrivateAddressError(nil))
	require.Contains(t, err.Error(), "169.254.169.254")
}

func TestIsBodyTooLargeError(t *testing.T) {
	err := &httpclient.BodyTooLargeError{Limit: 64}
	require.True(t, httpclient.IsBodyTooLargeError(err))
	require.True(t, httpclient.IsBodyTooLargeError(fmt.Errorf("read: %w", err)))
	require.False(t, httpclient.IsBodyTooLargeError(errors.New("other")))
	require.Contains(t, err.Error(), "64")
}

func TestIsUnhealthyStatusError(t *testing.T) {
	err := &httpclient.UnhealthyStatusError{StatusCode: 503}
	require.True(t, httpclient.IsUnhealthyStatusError(err))
	require.True(t, httpclient.IsUnhealthyStatusError(fmt.Errorf("probe: %w", err)))
	require.False(t, httpclient.IsUnhealthyStatusError(errors.New("other")))
	require.Contains(t, err.Error(), "503")
}
