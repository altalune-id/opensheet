//go:build !unix

package cli

import (
	"context"
	"errors"
)

func lockSessionFile(context.Context, string) (func(), error) {
	return nil, errors.New("locking the session file needs linux or darwin")
}
