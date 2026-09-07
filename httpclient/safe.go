package httpclient

import (
	"context"
	"net"
	"net/netip"
	"syscall"
)

var denyPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),          // RFC 1122 "this network"
	netip.MustParsePrefix("100.64.0.0/10"),      // RFC 6598 CGNAT
	netip.MustParsePrefix("192.0.0.0/24"),       // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),       // RFC 5737 TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),      // RFC 2544 benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),    // RFC 5737 TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),     // RFC 5737 TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),        // RFC 1112 reserved
	netip.MustParsePrefix("255.255.255.255/32"), // RFC 919 limited broadcast
	netip.MustParsePrefix("64:ff9b::/96"),       // RFC 6052 NAT64 well-known prefix
	netip.MustParsePrefix("64:ff9b:1::/48"),     // RFC 8215 NAT64 local-use prefix
	netip.MustParsePrefix("100::/64"),           // RFC 6666 discard-only
	netip.MustParsePrefix("2001::/32"),          // RFC 4380 Teredo
	netip.MustParsePrefix("2001:db8::/32"),      // RFC 3849 documentation
	netip.MustParsePrefix("2002::/16"),          // RFC 3056 6to4
	netip.MustParsePrefix("fec0::/10"),          // RFC 3879 deprecated site-local
}

// IsPrivateOrLocal reports whether addr falls in a range the SSRF dial filter refuses.
func IsPrivateOrLocal(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return true
	}
	unmapped := addr.Unmap()
	for _, p := range denyPrefixes {
		if p.Contains(unmapped) {
			return true
		}
	}
	return false
}

func rejectPrivate(_ context.Context, _, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return &PrivateAddressError{Address: address}
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return &PrivateAddressError{Address: address}
	}
	if IsPrivateOrLocal(addr) {
		return &PrivateAddressError{Address: address, Addr: addr}
	}
	return nil
}
