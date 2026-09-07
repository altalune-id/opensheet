package httpclient

import (
	"errors"
	"fmt"
	"net/netip"
)

// PrivateAddressError reports a dial refused because the destination resolved into a non-public range.
type PrivateAddressError struct {
	Address string
	Addr    netip.Addr
}

func (e *PrivateAddressError) Error() string {
	if !e.Addr.IsValid() {
		return fmt.Sprintf("httpclient: refused to dial %q: no usable IP", e.Address)
	}
	return fmt.Sprintf("httpclient: refused to dial private address %s (%s)", e.Addr, e.Address)
}

// IsPrivateAddressError reports whether err wraps a *PrivateAddressError.
func IsPrivateAddressError(err error) bool {
	var target *PrivateAddressError
	return errors.As(err, &target)
}

// BodyTooLargeError reports a response body that exceeded the client's configured limit.
type BodyTooLargeError struct {
	Limit int64
}

func (e *BodyTooLargeError) Error() string {
	return fmt.Sprintf("httpclient: response body exceeds %d bytes", e.Limit)
}

// IsBodyTooLargeError reports whether err wraps a *BodyTooLargeError.
func IsBodyTooLargeError(err error) bool {
	var target *BodyTooLargeError
	return errors.As(err, &target)
}

// UnhealthyStatusError reports a probe that reached its target but got a non-2xx status.
type UnhealthyStatusError struct {
	StatusCode int
}

func (e *UnhealthyStatusError) Error() string {
	return fmt.Sprintf("httpclient: unhealthy status %d", e.StatusCode)
}

// IsUnhealthyStatusError reports whether err wraps an *UnhealthyStatusError.
func IsUnhealthyStatusError(err error) bool {
	var target *UnhealthyStatusError
	return errors.As(err, &target)
}
