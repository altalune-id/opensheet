package apperror

import "errors"

// AsAppError walks the error chain returning the first *AppError, directly or via ToAppError() *AppError.
func AsAppError(err error) (*AppError, bool) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		//nolint:errorlint // hop-by-hop walk: need per-layer discovery of ToAppError producers.
		if ae, ok := e.(*AppError); ok {
			return ae, true
		}
		//nolint:errorlint // same rationale — checking the current hop, not the whole chain.
		if p, ok := e.(interface{ ToAppError() *AppError }); ok {
			return p.ToAppError(), true
		}
	}
	return nil, false
}
