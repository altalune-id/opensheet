package gsheet

import (
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"

	"altalune.id/opensheet/gworkspace"
)

type errStub struct{}

func (errStub) Error() string { return "stub" }

func TestTabNotFoundError_Message(t *testing.T) {
	if got := (&TabNotFoundError{Tab: "Q1"}).Error(); got != `gsheet: tab "Q1": not found` {
		t.Fatalf("Error() = %q", got)
	}
}

func TestIsTabNotFoundError(t *testing.T) {
	if !IsTabNotFoundError(&TabNotFoundError{}) {
		t.Error("IsTabNotFoundError rejected its own type")
	}
	if IsTabNotFoundError(errStub{}) {
		t.Error("IsTabNotFoundError(errStub) = true")
	}
	if IsTabNotFoundError(nil) {
		t.Error("IsTabNotFoundError(nil) = true")
	}
}

func TestTranslateRange(t *testing.T) {
	tab := translateRange(&googleapi.Error{Code: http.StatusBadRequest}, "FILE", "Q1")
	if !IsTabNotFoundError(tab) {
		t.Fatalf("400 = %v (%T), want TabNotFoundError", tab, tab)
	}
	gone := translateRange(&googleapi.Error{Code: http.StatusNotFound}, "FILE", "Q1")
	if !gworkspace.IsNotFoundError(gone) {
		t.Fatalf("404 = %v (%T), want gworkspace.NotFoundError", gone, gone)
	}
	other := translateRange(errStub{}, "FILE", "Q1")
	if !gworkspace.IsUnavailableError(other) {
		t.Fatalf("non-google error = %v (%T), want gworkspace.UnavailableError", other, other)
	}
}
