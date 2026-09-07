package onboard_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"altalune.id/template/internal/onboard"
)

func TestNotOnboardedError_Message(t *testing.T) {
	t.Parallel()
	e := &onboard.NotOnboardedError{}
	assert.Equal(t, "onboard: not onboarded", e.Error())
	assert.True(t, onboard.IsNotOnboardedError(e))
	assert.True(t, onboard.IsNotOnboardedError(errors.Join(e)))
	assert.False(t, onboard.IsNotOnboardedError(errors.New("boom")))
}

func TestAlreadyOnboardedError_Message(t *testing.T) {
	t.Parallel()
	e := &onboard.AlreadyOnboardedError{}
	assert.Equal(t, "onboard: already onboarded", e.Error())
	assert.True(t, onboard.IsAlreadyOnboardedError(e))
}

func TestInvalidMethodError_Message(t *testing.T) {
	t.Parallel()
	e := &onboard.InvalidMethodError{Method: "bad", Reason: "unknown method"}
	assert.Contains(t, e.Error(), "unknown method")
	assert.True(t, onboard.IsInvalidMethodError(e))

	noReason := &onboard.InvalidMethodError{Method: "x"}
	assert.Contains(t, noReason.Error(), "x")

	assert.NotNil(t, e.ToAppError())
}
