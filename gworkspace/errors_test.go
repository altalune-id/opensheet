package gworkspace

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

type errStub struct{}

func (errStub) Error() string { return "stub" }

func TestTypedErrors_Message(t *testing.T) {
	tests := []struct {
		err     error
		message string
	}{
		{&NotFoundError{FileID: "F"}, `gworkspace: document "F": not found`},
		{&PermissionDeniedError{FileID: "F"}, `gworkspace: document "F": permission denied`},
		{&QuotaExceededError{RetryAfter: 5 * time.Second}, "gworkspace: quota: exceeded, retry after 5s"},
		{&QuotaExceededError{}, "gworkspace: quota: exceeded"},
		{&UnavailableError{Cause: errStub{}}, "gworkspace: google api: stub"},
		{&UnavailableError{}, "gworkspace: google api: unavailable"},
		{&AuthExpiredError{Cause: errStub{}}, "gworkspace: credential: stub"},
		{&AuthExpiredError{}, "gworkspace: credential: expired"},
	}
	for _, tc := range tests {
		t.Run(tc.message, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.message {
				t.Errorf("Error() = %q, want %q", got, tc.message)
			}
		})
	}
}

func TestIsPredicates_RejectForeignErrors(t *testing.T) {
	preds := map[string]func(error) bool{
		"NotFound":         IsNotFoundError,
		"PermissionDenied": IsPermissionDeniedError,
		"QuotaExceeded":    IsQuotaExceededError,
		"Unavailable":      IsUnavailableError,
		"AuthExpired":      IsAuthExpiredError,
	}
	for name, pred := range preds {
		if pred(errStub{}) {
			t.Errorf("Is%sError(errStub) = true", name)
		}
		if pred(nil) {
			t.Errorf("Is%sError(nil) = true", name)
		}
	}
}

func TestIsPredicates_MatchTheirOwnType(t *testing.T) {
	cases := []struct {
		err  error
		pred func(error) bool
	}{
		{&NotFoundError{}, IsNotFoundError},
		{&PermissionDeniedError{}, IsPermissionDeniedError},
		{&QuotaExceededError{}, IsQuotaExceededError},
		{&UnavailableError{}, IsUnavailableError},
		{&AuthExpiredError{}, IsAuthExpiredError},
	}
	for _, tc := range cases {
		if !tc.pred(tc.err) {
			t.Errorf("predicate rejected %T", tc.err)
		}
	}
}

func TestErrors_UnwrapExposesCause(t *testing.T) {
	cause := errStub{}
	if !errors.Is(&UnavailableError{Cause: cause}, cause) {
		t.Error("UnavailableError does not unwrap to its cause")
	}
	if !errors.Is(&AuthExpiredError{Cause: cause}, cause) {
		t.Error("AuthExpiredError does not unwrap to its cause")
	}
}

func TestTranslate(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		check func(error) bool
	}{
		{"not a google api error", errStub{}, IsUnavailableError},
		{"404", &googleapi.Error{Code: http.StatusNotFound}, IsNotFoundError},
		{"403", &googleapi.Error{Code: http.StatusForbidden}, IsPermissionDeniedError},
		{"429", &googleapi.Error{Code: http.StatusTooManyRequests}, IsQuotaExceededError},
		{"401", &googleapi.Error{Code: http.StatusUnauthorized}, IsAuthExpiredError},
		{"400", &googleapi.Error{Code: http.StatusBadRequest}, IsUnavailableError},
		{"500", &googleapi.Error{Code: http.StatusInternalServerError}, IsUnavailableError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Translate(tc.err, "FILE")
			if !tc.check(got) {
				t.Fatalf("Translate(%v) = %v (%T), want the matching typed error", tc.err, got, got)
			}
		})
	}
}

func TestTranslate_CarriesTheFileID(t *testing.T) {
	nf, ok := errors.AsType[*NotFoundError](Translate(&googleapi.Error{Code: http.StatusNotFound}, "FILE"))
	if !ok {
		t.Fatal("Translate did not produce a *NotFoundError")
	}
	if nf.FileID != "FILE" {
		t.Fatalf("FileID = %q, want FILE", nf.FileID)
	}
	pd, ok := errors.AsType[*PermissionDeniedError](Translate(&googleapi.Error{Code: http.StatusForbidden}, "FILE"))
	if !ok {
		t.Fatal("Translate did not produce a *PermissionDeniedError")
	}
	if pd.FileID != "FILE" {
		t.Fatalf("FileID = %q, want FILE", pd.FileID)
	}
}

func TestRetryAfter(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"absent", "", false},
		{"seconds", "30", true},
		{"zero seconds", "0", false},
		{"http date in the past", "Mon, 02 Jan 2006 15:04:05 GMT", false},
		{"garbage", "soon", false},
		{"http date in the future", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &googleapi.Error{Header: http.Header{}}
			if tc.raw != "" {
				g.Header.Set("Retry-After", tc.raw)
			}
			if got := retryAfter(g); (got > 0) != tc.want {
				t.Fatalf("retryAfter(%q) = %v, want positive = %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestQuotaExceeded_CarriesRetryAfter(t *testing.T) {
	g := &googleapi.Error{Code: http.StatusTooManyRequests, Header: http.Header{}}
	g.Header.Set("Retry-After", "30")

	quota, ok := errors.AsType[*QuotaExceededError](Translate(g, "FILE"))
	if !ok {
		t.Fatal("Translate did not produce a *QuotaExceededError")
	}
	if quota.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %v, want 30s", quota.RetryAfter)
	}
}
