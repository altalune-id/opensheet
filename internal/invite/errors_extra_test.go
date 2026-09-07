package invite_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"altalune.id/template/internal/invite"
)

func TestErrors_EmptyMessages(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "invite: not found", (&invite.NotFoundError{}).Error())
	assert.Equal(t, "invite: expired", (&invite.ExpiredError{}).Error())
	assert.Equal(t, "invite: already used", (&invite.AlreadyUsedError{}).Error())
	assert.Equal(t, "invite: token mismatch", (&invite.TokenMismatchError{}).Error())
}

func TestErrors_FormattedMessages(t *testing.T) {
	t.Parallel()
	assert.Contains(t, (&invite.NotFoundError{ID: "x"}).Error(), "x")
	assert.Contains(t, (&invite.ExpiredError{ID: "x"}).Error(), "x")
	assert.Contains(t, (&invite.AlreadyUsedError{ID: "x"}).Error(), "x")
	assert.Contains(t, (&invite.InvalidRoleError{Role: "x"}).Error(), "x")
	assert.Contains(t, (&invite.InvalidEmailError{Value: "bad@", Reason: "malformed"}).Error(), "malformed")
	assert.Contains(t, (&invite.InvalidEmailError{}).Error(), "invalid")
}

func TestErrors_ToAppErrorAll(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, (&invite.NotFoundError{}).ToAppError())
	assert.NotNil(t, (&invite.ExpiredError{}).ToAppError())
	assert.NotNil(t, (&invite.AlreadyUsedError{}).ToAppError())
	assert.NotNil(t, (&invite.InvalidRoleError{}).ToAppError())
	assert.NotNil(t, (&invite.InvalidEmailError{}).ToAppError())
	assert.NotNil(t, (&invite.TokenMismatchError{}).ToAppError())
}
