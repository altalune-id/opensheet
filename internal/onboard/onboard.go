// Package onboard models the singleton bootstrap record that marks first-run completion.
package onboard

import (
	"time"

	"github.com/google/uuid"
)

// Method names the flow that completed onboarding.
type Method string

const (
	MethodEnvGenesis Method = "env-genesis"
	MethodWebOnboard Method = "web-onboard"
	MethodCLIInit    Method = "cli-init"
)

// IsValid reports whether m is one of the recognised methods.
func (m Method) IsValid() bool {
	switch m {
	case MethodEnvGenesis, MethodWebOnboard, MethodCLIInit:
		return true
	}
	return false
}

// Bootstrap is the singleton aggregate that marks the deployment as onboarded.
type Bootstrap struct {
	OnboardedAt time.Time
	OnboardedBy uuid.UUID
	Method      Method
}

// New builds a Bootstrap and enforces invariants.
func New(by uuid.UUID, method Method, now time.Time) (*Bootstrap, error) {
	if by == uuid.Nil {
		return nil, &InvalidMethodError{Method: string(method), Reason: "onboarded_by required"}
	}
	if !method.IsValid() {
		return nil, &InvalidMethodError{Method: string(method), Reason: "unknown method"}
	}
	if now.IsZero() {
		return nil, &InvalidMethodError{Method: string(method), Reason: "timestamp required"}
	}
	return &Bootstrap{
		OnboardedAt: now.UTC(),
		OnboardedBy: by,
		Method:      method,
	}, nil
}
