package org_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
)

func TestErrorCodes_ProjectGrantRegistered(t *testing.T) {
	codesSrc, err := os.ReadFile(filepath.Join("..", "apperror", "codes.go"))
	require.NoError(t, err)
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "errors", "opensheet.md"))
	require.NoError(t, err)

	for _, c := range []struct {
		err  interface{ ToAppError() *apperror.AppError }
		code string
		grpc codes.Code
		is   func(error) bool
	}{
		{&org.ProjectGrantEmptyError{}, "ROL010", codes.InvalidArgument, org.IsProjectGrantEmptyError},
		{&org.ProjectGrantConflictError{}, "ROL011", codes.InvalidArgument, org.IsProjectGrantConflictError},
		{&org.ProjectGrantEscalationError{}, "ROL012", codes.PermissionDenied, org.IsProjectGrantEscalationError},
	} {
		assert.Regexp(t, regexp.MustCompile(`(?m)^\tCode\w+\s+=\s+"`+c.code+`"`), string(codesSrc), "%s is not in codes.go", c.code)
		assert.Regexp(t, "(?m)^\\|\\s*`"+c.code+"`\\s*\\|", string(doc), "%s has no row in docs/errors/opensheet.md", c.code)
		ae := c.err.ToAppError()
		assert.Equal(t, c.code, ae.Code())
		assert.Equal(t, c.grpc, ae.GRPCCode(), c.code)
		assert.NotEmpty(t, ae.Message(), c.code)
		assert.True(t, c.is(c.err.(error)), c.code)
	}
}
