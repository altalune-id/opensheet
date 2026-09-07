package httpclient_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/httpclient"
)

func TestIsPrivateOrLocal(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"ipv4 loopback", "127.0.0.1", true},
		{"ipv4 loopback high", "127.255.255.254", true},
		{"ipv4 unspecified", "0.0.0.0", true},
		{"ipv4 this network", "0.1.2.3", true},
		{"ipv4 private 10", "10.0.0.1", true},
		{"ipv4 private 172", "172.16.0.1", true},
		{"ipv4 private 192", "192.168.1.1", true},
		{"ipv4 link local", "169.254.169.254", true},
		{"ipv4 cgnat", "100.100.100.200", true},
		{"ipv4 protocol assignments", "192.0.0.1", true},
		{"ipv4 test net 1", "192.0.2.5", true},
		{"ipv4 benchmark", "198.19.0.1", true},
		{"ipv4 test net 2", "198.51.100.5", true},
		{"ipv4 test net 3", "203.0.113.5", true},
		{"ipv4 multicast", "224.0.0.1", true},
		{"ipv4 reserved", "240.0.0.1", true},
		{"ipv4 broadcast", "255.255.255.255", true},
		{"ipv6 loopback", "::1", true},
		{"ipv6 unspecified", "::", true},
		{"ipv6 ula", "fd00::1", true},
		{"ipv6 link local", "fe80::1", true},
		{"ipv6 site local deprecated", "fec0::1", true},
		{"ipv6 discard only", "100::1", true},
		{"ipv6 nat64 wellknown", "64:ff9b::a00:1", true},
		{"ipv6 nat64 local use", "64:ff9b:1::1", true},
		{"ipv6 teredo", "2001:0:1234::1", true},
		{"ipv6 documentation", "2001:db8::1", true},
		{"ipv6 6to4", "2002:a00:1::1", true},
		{"ipv6 multicast", "ff02::1", true},
		{"ipv6 interface local multicast", "ff01::1", true},
		{"4in6 loopback", "::ffff:127.0.0.1", true},
		{"4in6 private", "::ffff:10.0.0.1", true},
		{"4in6 link local", "::ffff:169.254.169.254", true},
		{"4in6 cgnat", "::ffff:100.64.0.1", true},
		{"public ipv4 google dns", "8.8.8.8", false},
		{"public ipv4 cloudflare", "1.1.1.1", false},
		{"public ipv4 above cgnat", "100.128.0.1", false},
		{"public ipv4 below benchmark", "198.17.255.255", false},
		{"public ipv6 google dns", "2001:4860:4860::8888", false},
		{"public ipv6 above teredo", "2001:1::1", false},
		{"4in6 public", "::ffff:8.8.8.8", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.addr)
			require.NoError(t, err)
			require.Equal(t, tt.want, httpclient.IsPrivateOrLocal(addr))
		})
	}
}

func TestIsPrivateOrLocal_InvalidAddrIsDenied(t *testing.T) {
	require.True(t, httpclient.IsPrivateOrLocal(netip.Addr{}))
}
