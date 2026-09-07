package user_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/user"
)

func TestNotFoundError_Error(t *testing.T) {
	cases := []struct {
		name string
		err  *user.NotFoundError
		want string
	}{
		{"by id", &user.NotFoundError{ID: "abc"}, "user: not found: id=abc"},
		{"by email", &user.NotFoundError{Email: "a@b.co"}, "user: not found: email=a@b.co"},
		{"bare", &user.NotFoundError{}, "user: not found"},
		{"id wins over email", &user.NotFoundError{ID: "abc", Email: "a@b.co"}, "user: not found: id=abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestNotFoundError_ToAppError(t *testing.T) {
	err := (&user.NotFoundError{ID: "x"}).ToAppError()
	require.NotNil(t, err)
	assert.Equal(t, apperror.CodeUserNotFound, err.Code())
	assert.Equal(t, codes.NotFound, err.GRPCCode())
}

func TestIsNotFoundError(t *testing.T) {
	assert.True(t, user.IsNotFoundError(&user.NotFoundError{}))
	assert.True(t, user.IsNotFoundError(fmt.Errorf("wrap: %w", &user.NotFoundError{})))
	assert.False(t, user.IsNotFoundError(errors.New("plain")))
	assert.False(t, user.IsNotFoundError(nil))
}

func TestAlreadyExistsError_Error(t *testing.T) {
	cases := []struct {
		name string
		err  *user.AlreadyExistsError
		want string
	}{
		{"with field", &user.AlreadyExistsError{Field: "email", Value: "a@b"}, `user: already exists: email="a@b"`},
		{"bare", &user.AlreadyExistsError{}, "user: already exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestAlreadyExistsError_ToAppError(t *testing.T) {
	err := (&user.AlreadyExistsError{Field: "email", Value: "a@b"}).ToAppError()
	require.NotNil(t, err)
	assert.Equal(t, apperror.CodeUserAlreadyExists, err.Code())
	assert.Equal(t, codes.AlreadyExists, err.GRPCCode())
}

func TestIsAlreadyExistsError(t *testing.T) {
	assert.True(t, user.IsAlreadyExistsError(&user.AlreadyExistsError{}))
	assert.True(t, user.IsAlreadyExistsError(fmt.Errorf("wrap: %w", &user.AlreadyExistsError{})))
	assert.False(t, user.IsAlreadyExistsError(errors.New("plain")))
	assert.False(t, user.IsAlreadyExistsError(nil))
}

func TestNotInvitedError_Error(t *testing.T) {
	cases := []struct {
		name string
		err  *user.NotInvitedError
		want string
	}{
		{"with email", &user.NotInvitedError{Email: "a@b"}, "user: not invited: email=a@b"},
		{"bare", &user.NotInvitedError{}, "user: not invited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestNotInvitedError_ToAppError(t *testing.T) {
	err := (&user.NotInvitedError{Email: "a@b"}).ToAppError()
	require.NotNil(t, err)
	assert.Equal(t, apperror.CodeUserNotInvited, err.Code())
	assert.Equal(t, codes.PermissionDenied, err.GRPCCode())
}

func TestIsNotInvitedError(t *testing.T) {
	assert.True(t, user.IsNotInvitedError(&user.NotInvitedError{}))
	assert.True(t, user.IsNotInvitedError(fmt.Errorf("wrap: %w", &user.NotInvitedError{})))
	assert.False(t, user.IsNotInvitedError(errors.New("plain")))
	assert.False(t, user.IsNotInvitedError(nil))
}

func TestInvalidEmailError_Error(t *testing.T) {
	cases := []struct {
		name string
		err  *user.InvalidEmailError
		want string
	}{
		{"with reason and value", &user.InvalidEmailError{Reason: "bad", Value: "x"}, `user: email: bad: "x"`},
		{"empty reason defaults", &user.InvalidEmailError{Value: "x"}, `user: email: invalid: "x"`},
		{"empty value", &user.InvalidEmailError{Reason: "empty"}, "user: email: empty"},
		{"bare", &user.InvalidEmailError{}, "user: email: invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestInvalidEmailError_ToAppError(t *testing.T) {
	err := (&user.InvalidEmailError{Reason: "bad"}).ToAppError()
	require.NotNil(t, err)
	assert.Equal(t, apperror.CodeUserInvalidEmail, err.Code())
	assert.Equal(t, codes.InvalidArgument, err.GRPCCode())
}

func TestIsInvalidEmailError(t *testing.T) {
	assert.True(t, user.IsInvalidEmailError(&user.InvalidEmailError{}))
	assert.True(t, user.IsInvalidEmailError(fmt.Errorf("wrap: %w", &user.InvalidEmailError{})))
	assert.False(t, user.IsInvalidEmailError(errors.New("plain")))
	assert.False(t, user.IsInvalidEmailError(nil))
}

func TestInvalidNameError_Error(t *testing.T) {
	cases := []struct {
		name string
		err  *user.InvalidNameError
		want string
	}{
		{"with reason", &user.InvalidNameError{Reason: "empty"}, "user: name: empty"},
		{"bare", &user.InvalidNameError{}, "user: name: invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}

func TestInvalidNameError_ToAppError(t *testing.T) {
	err := (&user.InvalidNameError{Reason: "empty"}).ToAppError()
	require.NotNil(t, err)
	assert.Equal(t, apperror.CodeUserInvalidName, err.Code())
	assert.Equal(t, codes.InvalidArgument, err.GRPCCode())
}

func TestIsInvalidNameError(t *testing.T) {
	assert.True(t, user.IsInvalidNameError(&user.InvalidNameError{}))
	assert.True(t, user.IsInvalidNameError(fmt.Errorf("wrap: %w", &user.InvalidNameError{})))
	assert.False(t, user.IsInvalidNameError(errors.New("plain")))
	assert.False(t, user.IsInvalidNameError(nil))
}
