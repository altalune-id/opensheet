package cli

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/config"
)

const unauthenticatedHint = "not signed in or credential rejected; run `opensheet auth login --url <url>` or pass --token"

func withHint(err error) error {
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		return err
	}
	return apperror.New(apperror.CodeUnauthenticated, unauthenticatedHint, codes.Unauthenticated).WithCause(err)
}

func unauthenticated(err error) *apperror.AppError {
	return apperror.New(apperror.CodeUnauthenticated, err.Error(), codes.Unauthenticated)
}

// ProfileHostMismatchError reports a profile signed in to another server than the one requested. SECURITY: its token is not sent.
type ProfileHostMismatchError struct {
	Profile    string
	ProfileURL string
	URL        string
}

func (e *ProfileHostMismatchError) Error() string {
	return fmt.Sprintf("profile %s is signed in to %s, not %s; run `opensheet auth login --url %s --profile %s`",
		e.Profile, e.ProfileURL, e.URL, e.URL, e.Profile)
}

// ToAppError maps the mismatch to Unauthenticated, so it exits like a rejected credential.
func (e *ProfileHostMismatchError) ToAppError() *apperror.AppError { return unauthenticated(e) }

// IsProfileHostMismatchError reports whether err is a *ProfileHostMismatchError.
func IsProfileHostMismatchError(err error) bool {
	_, ok := errors.AsType[*ProfileHostMismatchError](err)
	return ok
}

// SessionExpiredError reports a profile whose refresh token the issuer no longer honours; the person signs in again.
type SessionExpiredError struct {
	Profile string
	URL     string
	Err     error
}

func (e *SessionExpiredError) Error() string {
	return fmt.Sprintf("session for profile %s expired; run `opensheet auth login --url %s --profile %s`", e.Profile, e.URL, e.Profile)
}

func (e *SessionExpiredError) Unwrap() error { return e.Err }

// ToAppError maps the expiry to Unauthenticated.
func (e *SessionExpiredError) ToAppError() *apperror.AppError { return unauthenticated(e) }

// IsSessionExpiredError reports whether err is a *SessionExpiredError.
func IsSessionExpiredError(err error) bool {
	_, ok := errors.AsType[*SessionExpiredError](err)
	return ok
}

// NotSignedInError reports a profile holding no credential for the command: a remote sign-in for URL, or a local one when URL is empty.
type NotSignedInError struct {
	Profile string
	URL     string
}

func (e *NotSignedInError) Error() string {
	if e.URL == "" {
		return fmt.Sprintf("not signed in on this server (profile %s); run `opensheet auth login --profile %s` without --url", e.Profile, e.Profile)
	}
	return fmt.Sprintf("profile %s is not signed in to %s; run `opensheet auth login --url %s --profile %s` or pass --token",
		e.Profile, e.URL, e.URL, e.Profile)
}

// ToAppError maps the missing sign-in to Unauthenticated.
func (e *NotSignedInError) ToAppError() *apperror.AppError { return unauthenticated(e) }

// IsNotSignedInError reports whether err is a *NotSignedInError.
func IsNotSignedInError(err error) bool {
	_, ok := errors.AsType[*NotSignedInError](err)
	return ok
}

// UserNotProvisionedError reports a sign-in the issuer accepted for a person the app has no user for yet.
type UserNotProvisionedError struct{ URL string }

func (e *UserNotProvisionedError) Error() string {
	return fmt.Sprintf("signed in to the identity provider, but %s doesn't know you yet: sign in once at %s in a browser, then run `opensheet auth login --url %s` again",
		e.URL, e.URL, e.URL)
}

// ToAppError maps the unknown user to Unauthenticated.
func (e *UserNotProvisionedError) ToAppError() *apperror.AppError { return unauthenticated(e) }

// IsUserNotProvisionedError reports whether err is a *UserNotProvisionedError.
func IsUserNotProvisionedError(err error) bool {
	_, ok := errors.AsType[*UserNotProvisionedError](err)
	return ok
}

// ResourceMetadataError reports an app whose RFC 9728 document cannot start a sign-in.
type ResourceMetadataError struct {
	URL    string
	Reason string
	Err    error
}

func (e *ResourceMetadataError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("login: %s: %s: %v", e.URL, e.Reason, e.Err)
	}
	return fmt.Sprintf("login: %s: %s", e.URL, e.Reason)
}

func (e *ResourceMetadataError) Unwrap() error { return e.Err }

// IsResourceMetadataError reports whether err is a *ResourceMetadataError.
func IsResourceMetadataError(err error) bool {
	_, ok := errors.AsType[*ResourceMetadataError](err)
	return ok
}

// InsecureURLError reports a server URL that is neither https nor http on a loopback host.
type InsecureURLError struct{ URL string }

func (e *InsecureURLError) Error() string {
	return fmt.Sprintf("%s must use https (plain http only on a loopback host)", e.URL)
}

// ToAppError maps the URL to InvalidArgument.
func (e *InsecureURLError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeValidation, e.Error(), codes.InvalidArgument)
}

// IsInsecureURLError reports whether err is an *InsecureURLError.
func IsInsecureURLError(err error) bool {
	_, ok := errors.AsType[*InsecureURLError](err)
	return ok
}

// MalformedURLError reports a server URL that is not an absolute http(s) URL free of userinfo, query and fragment.
type MalformedURLError struct{ URL string }

func (e *MalformedURLError) Error() string {
	return fmt.Sprintf("%s must be an absolute http(s) URL with no userinfo, query or fragment", e.URL)
}

// ToAppError maps the URL to InvalidArgument.
func (e *MalformedURLError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeValidation, e.Error(), codes.InvalidArgument)
}

// IsMalformedURLError reports whether err is a *MalformedURLError.
func IsMalformedURLError(err error) bool {
	_, ok := errors.AsType[*MalformedURLError](err)
	return ok
}

// MissingURLError reports a remote command with no server URL from a flag, the environment, the profile or config.
type MissingURLError struct{}

func (e *MissingURLError) Error() string {
	return "no server URL; pass --url or set " + config.EnvVar("url")
}

// ToAppError maps the missing URL to InvalidArgument.
func (e *MissingURLError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeValidation, e.Error(), codes.InvalidArgument)
}

// IsMissingURLError reports whether err is a *MissingURLError.
func IsMissingURLError(err error) bool {
	_, ok := errors.AsType[*MissingURLError](err)
	return ok
}

// RemoteFlagError reports a remote flag on a command that runs in-process against the database.
type RemoteFlagError struct {
	Command string
	Flag    string
}

func (e *RemoteFlagError) Error() string {
	return fmt.Sprintf("%s runs on the server with database access; it can't use --%s. Run it on the server without --url, --token or --token-file",
		e.Command, e.Flag)
}

// ToAppError maps the flag to InvalidArgument.
func (e *RemoteFlagError) ToAppError() *apperror.AppError {
	return apperror.New(apperror.CodeValidation, e.Error(), codes.InvalidArgument)
}

// IsRemoteFlagError reports whether err is a *RemoteFlagError.
func IsRemoteFlagError(err error) bool {
	_, ok := errors.AsType[*RemoteFlagError](err)
	return ok
}

// SessionFileError reports a session file that cannot be read, written or locked.
type SessionFileError struct {
	Path string
	Err  error
}

func (e *SessionFileError) Error() string {
	return fmt.Sprintf("cli: session file %s: %v", e.Path, e.Err)
}

func (e *SessionFileError) Unwrap() error { return e.Err }

// IsSessionFileError reports whether err is a *SessionFileError.
func IsSessionFileError(err error) bool {
	_, ok := errors.AsType[*SessionFileError](err)
	return ok
}
