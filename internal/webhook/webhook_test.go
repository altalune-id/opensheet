package webhook_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/events"
	"altalune.id/opensheet/internal/webhook"
)

const validURL = "https://example.com/hook"

func validTypes() []events.Type { return []events.Type{events.PostPublished} }

func TestNew_URLRules(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https accepted", url: validURL},
		{name: "https with port and query accepted", url: "https://example.com:8443/hook?x=1"},
		{name: "exactly 2048 characters accepted", url: "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))},
		{name: "http rejected", url: "http://example.com/hook", wantErr: true},
		{name: "other scheme rejected", url: "ftp://example.com/hook", wantErr: true},
		{name: "userinfo rejected", url: "https://user:pass@example.com/hook", wantErr: true},
		{name: "username only rejected", url: "https://user@example.com/hook", wantErr: true},
		{name: "2049 characters rejected", url: "https://example.com/" + strings.Repeat("a", 2049-len("https://example.com/")), wantErr: true},
		{name: "no host rejected", url: "https:///hook", wantErr: true},
		{name: "port without host rejected", url: "https://:443/hook", wantErr: true},
		{name: "empty rejected", url: "", wantErr: true},
		{name: "relative rejected", url: "/hook", wantErr: true},
		{name: "unparsable rejected", url: "https://exa mple.com/%zz", wantErr: true},
		{name: "invalid utf-8 rejected", url: "https://example.com/\xff", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := webhook.New(uuid.New(), uuid.New(), tt.url, "", validTypes())
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, webhook.IsInvalidURLError(err), "want *InvalidURLError, got %T: %v", err, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.url, e.URL)
		})
	}
}

func TestNew_EventTypeRules(t *testing.T) {
	tests := []struct {
		name    string
		types   []events.Type
		wantErr bool
	}{
		{name: "one subscribable type", types: []events.Type{events.PostPublished}},
		{name: "every subscribable type", types: []events.Type{events.PostPublished, events.PostUnpublished, events.PostDeleted}},
		{name: "nil rejected", types: nil, wantErr: true},
		{name: "empty rejected", types: []events.Type{}, wantErr: true},
		{name: "duplicate rejected", types: []events.Type{events.PostPublished, events.PostPublished}, wantErr: true},
		{name: "ping is not subscribable", types: []events.Type{events.WebhookPing}, wantErr: true},
		{name: "unknown rejected", types: []events.Type{"blog.post.exploded"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := webhook.New(uuid.New(), uuid.New(), validURL, "", tt.types)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, webhook.IsInvalidEventTypesError(err), "want *InvalidEventTypesError, got %T: %v", err, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.types, e.EventTypes)
		})
	}
}

func TestNew_EventTypesAreCopied(t *testing.T) {
	types := []events.Type{events.PostPublished}
	e, err := webhook.New(uuid.New(), uuid.New(), validURL, "", types)
	require.NoError(t, err)
	types[0] = events.PostDeleted
	assert.Equal(t, []events.Type{events.PostPublished}, e.EventTypes, "the aggregate must not alias the caller's slice")
}

func TestNew_DescriptionRules(t *testing.T) {
	tests := []struct {
		name    string
		desc    string
		want    string
		wantErr bool
	}{
		{name: "empty accepted", desc: "", want: ""},
		{name: "trimmed", desc: "  Orders  ", want: "Orders"},
		{name: "200 runes accepted", desc: strings.Repeat("é", 200), want: strings.Repeat("é", 200)},
		{name: "201 runes rejected", desc: strings.Repeat("é", 201), wantErr: true},
		{name: "invalid utf-8 rejected", desc: "bad \xff", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := webhook.New(uuid.New(), uuid.New(), validURL, tt.desc, validTypes())
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, webhook.IsInvalidDescriptionError(err), "want *InvalidDescriptionError, got %T: %v", err, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, e.Description)
		})
	}
}

func TestNew_Defaults(t *testing.T) {
	org, proj := uuid.New(), uuid.New()
	before := time.Now().UTC()
	e, err := webhook.New(org, proj, validURL, "d", validTypes())
	require.NoError(t, err)

	assert.NotEqual(t, uuid.Nil, e.ID)
	assert.Equal(t, org, e.OrgID)
	assert.Equal(t, proj, e.ProjectID)
	assert.True(t, e.Active, "a new endpoint starts active")
	assert.Equal(t, time.UTC, e.CreatedAt.Location())
	assert.Equal(t, time.UTC, e.UpdatedAt.Location())
	assert.False(t, e.CreatedAt.Before(before))
	assert.Equal(t, e.CreatedAt, e.UpdatedAt)
	assert.Empty(t, e.Secrets.Primary, "sealing secrets is the service's job")
	assert.Nil(t, e.Secrets.Secondary)
}

func TestEndpoint_Subscribes(t *testing.T) {
	e, err := webhook.New(uuid.New(), uuid.New(), validURL, "", []events.Type{events.PostPublished, events.PostDeleted})
	require.NoError(t, err)

	assert.True(t, e.Subscribes(events.PostPublished))
	assert.True(t, e.Subscribes(events.PostDeleted))
	assert.False(t, e.Subscribes(events.PostUnpublished))
	assert.False(t, e.Subscribes(events.WebhookPing))
}

func TestEndpoint_Update(t *testing.T) {
	newEndpoint := func(t *testing.T) *webhook.Endpoint {
		t.Helper()
		e, err := webhook.New(uuid.New(), uuid.New(), validURL, "old", validTypes())
		require.NoError(t, err)
		e.UpdatedAt = e.UpdatedAt.Add(-time.Hour)
		return e
	}

	t.Run("replaces fields and bumps UpdatedAt", func(t *testing.T) {
		e := newEndpoint(t)
		created, prevUpdated := e.CreatedAt, e.UpdatedAt
		types := []events.Type{events.PostDeleted, events.PostUnpublished}

		require.NoError(t, e.Update("https://example.org/new", " new ", types, false))

		assert.Equal(t, "https://example.org/new", e.URL)
		assert.Equal(t, "new", e.Description)
		assert.Equal(t, types, e.EventTypes)
		assert.False(t, e.Active)
		assert.Equal(t, created, e.CreatedAt)
		assert.True(t, e.UpdatedAt.After(prevUpdated))
		assert.Equal(t, time.UTC, e.UpdatedAt.Location())
	})

	t.Run("reactivates", func(t *testing.T) {
		e := newEndpoint(t)
		require.NoError(t, e.Update(validURL, "", validTypes(), false))
		require.NoError(t, e.Update(validURL, "", validTypes(), true))
		assert.True(t, e.Active)
	})

	invalid := []struct {
		name  string
		url   string
		desc  string
		types []events.Type
		is    func(error) bool
	}{
		{name: "bad url", url: "http://example.com", types: validTypes(), is: webhook.IsInvalidURLError},
		{name: "bad types", url: validURL, types: []events.Type{events.WebhookPing}, is: webhook.IsInvalidEventTypesError},
		{name: "bad description", url: validURL, desc: strings.Repeat("x", 201), types: validTypes(), is: webhook.IsInvalidDescriptionError},
	}
	for _, tt := range invalid {
		t.Run("rejects "+tt.name+" and leaves the endpoint untouched", func(t *testing.T) {
			e := newEndpoint(t)
			before := *e
			err := e.Update(tt.url, tt.desc, tt.types, false)
			require.Error(t, err)
			assert.True(t, tt.is(err), "got %T: %v", err, err)
			assert.Equal(t, before, *e)
		})
	}
}

func TestErrors_ToAppError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
		grpc codes.Code
	}{
		{name: "not found", err: &webhook.NotFoundError{ID: "x"}, code: apperror.CodeWebhookEndpointNotFound, grpc: codes.NotFound},
		{name: "invalid url", err: &webhook.InvalidURLError{Reason: "r"}, code: apperror.CodeWebhookInvalidURL, grpc: codes.InvalidArgument},
		{name: "invalid event types", err: &webhook.InvalidEventTypesError{Reason: "r"}, code: apperror.CodeWebhookInvalidEventTypes, grpc: codes.InvalidArgument},
		{name: "invalid description", err: &webhook.InvalidDescriptionError{Reason: "r"}, code: apperror.CodeValidation, grpc: codes.InvalidArgument},
		{name: "endpoint limit", err: &webhook.EndpointLimitError{Limit: webhook.MaxEndpointsPerProject}, code: apperror.CodeWebhookEndpointLimit, grpc: codes.FailedPrecondition},
		{name: "not retryable", err: &webhook.DeliveryNotRetryableError{ID: "x"}, code: apperror.CodeWebhookDeliveryNotRetryable, grpc: codes.FailedPrecondition},
		{name: "delivery not found", err: &webhook.DeliveryNotFoundError{ID: "x"}, code: apperror.CodeWebhookDeliveryNotFound, grpc: codes.NotFound},
		{name: "inactive", err: &webhook.EndpointInactiveError{ID: "x"}, code: apperror.CodeWebhookEndpointInactive, grpc: codes.FailedPrecondition},
		{name: "secret conflict", err: &webhook.SecretConflictError{ID: "x"}, code: apperror.CodeWebhookSecretConflict, grpc: codes.FailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ae, ok := apperror.AsAppError(tt.err)
			require.True(t, ok)
			assert.Equal(t, tt.code, ae.Code())
			assert.Equal(t, tt.grpc, ae.GRPCCode())
			assert.True(t, strings.HasPrefix(tt.err.Error(), "webhook: "), tt.err.Error())
		})
	}
}

func TestErrors_Helpers(t *testing.T) {
	cause := &webhook.SecretUnavailableError{Cause: assert.AnError}
	assert.True(t, webhook.IsSecretUnavailableError(cause))
	assert.ErrorIs(t, cause, assert.AnError)
	assert.Contains(t, cause.Error(), "rotate")

	assert.True(t, webhook.IsNotFoundError(wrap(&webhook.NotFoundError{})))
	assert.True(t, webhook.IsSecretConflictError(wrap(&webhook.SecretConflictError{})))
	assert.True(t, webhook.IsInvalidURLError(wrap(&webhook.InvalidURLError{})))
	assert.True(t, webhook.IsInvalidEventTypesError(wrap(&webhook.InvalidEventTypesError{})))
	assert.True(t, webhook.IsInvalidDescriptionError(wrap(&webhook.InvalidDescriptionError{})))
	assert.True(t, webhook.IsEndpointLimitError(wrap(&webhook.EndpointLimitError{})))
	assert.True(t, webhook.IsDeliveryNotRetryableError(wrap(&webhook.DeliveryNotRetryableError{})))
	assert.True(t, webhook.IsDeliveryNotFoundError(wrap(&webhook.DeliveryNotFoundError{})))
	assert.True(t, webhook.IsEndpointInactiveError(wrap(&webhook.EndpointInactiveError{})))
	assert.True(t, webhook.IsInvalidAttemptError(wrap(&webhook.InvalidAttemptError{})))
	assert.True(t, webhook.IsNoUnitOfWorkError(wrap(&webhook.NoUnitOfWorkError{})))
	assert.True(t, webhook.IsDeliveryFailedError(wrap(&webhook.DeliveryFailedError{StatusCode: 500})))
	assert.Contains(t, (&webhook.DeliveryFailedError{StatusCode: 503}).Error(), "503")
	assert.False(t, webhook.IsNotFoundError(assert.AnError))

	for _, err := range []error{
		&webhook.InvalidAttemptError{}, &webhook.NoUnitOfWorkError{},
		&webhook.SecretUnavailableError{}, &webhook.DeliveryFailedError{},
	} {
		_, ok := err.(interface{ ToAppError() *apperror.AppError })
		assert.False(t, ok, "%T is an internal failure and must carry no code", err)
	}
}

func wrap(err error) error { return fmt.Errorf("outer: %w", err) }

func TestEndpoint_SetSecrets(t *testing.T) {
	e, err := webhook.New(uuid.New(), uuid.New(), validURL, "", validTypes())
	require.NoError(t, err)
	e.UpdatedAt = time.Now().Add(-time.Hour).UTC()
	before := e.UpdatedAt

	e.SetSecrets(webhook.SealedSecrets{Primary: []byte("p"), Secondary: []byte("s")})
	assert.Equal(t, webhook.SealedSecrets{Primary: []byte("p"), Secondary: []byte("s")}, e.Secrets)
	assert.True(t, e.UpdatedAt.After(before))
	assert.Equal(t, time.UTC, e.UpdatedAt.Location())

	e.SetSecrets(webhook.SealedSecrets{Primary: []byte("p")})
	assert.Nil(t, e.Secrets.Secondary)
}
