package gworkspace

import (
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type staticTokenSource struct{}

func (staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "dummy", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}, nil
}

func TestClientOptions_RejectsNilTokenSource(t *testing.T) {
	if _, err := ClientOptions(nil, "scope"); err == nil {
		t.Fatal("ClientOptions(nil) succeeded")
	}
}

func TestClientOptions_AppendsEndpointOnlyWhenOverridden(t *testing.T) {
	plain, err := ClientOptions(staticTokenSource{}, "scope")
	if err != nil {
		t.Fatalf("ClientOptions: %v", err)
	}
	if len(plain) != 2 {
		t.Fatalf("got %d options, want 2 (http client + scopes)", len(plain))
	}

	overridden, err := ClientOptions(staticTokenSource{}, "scope", WithBaseURL("http://127.0.0.1:1/"))
	if err != nil {
		t.Fatalf("ClientOptions: %v", err)
	}
	if len(overridden) != 3 {
		t.Fatalf("got %d options, want 3 (http client + scopes + endpoint)", len(overridden))
	}
}

func TestSettings_Endpoint(t *testing.T) {
	tests := map[string]string{
		"":                 "",
		"   ":              "",
		"http://host":      "http://host/",
		"http://host/":     "http://host/",
		"  http://host/  ": "http://host/",
	}
	for in, want := range tests {
		var s settings
		WithBaseURL(in)(&s)
		if got := s.endpoint(); got != want {
			t.Errorf("WithBaseURL(%q).endpoint() = %q, want %q", in, got, want)
		}
	}
}

func TestSettings_HTTPClientCarriesTheTokenSource(t *testing.T) {
	ts := staticTokenSource{}
	hc := settings{timeout: 7 * time.Second}.httpClient(ts)

	if hc.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want 7s", hc.Timeout)
	}
	tr, ok := hc.Transport.(*oauth2.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *oauth2.Transport", hc.Transport)
	}
	if tr.Source != oauth2.TokenSource(ts) {
		t.Errorf("Transport.Source = %v, want the supplied token source", tr.Source)
	}
	if tr.Base == nil {
		t.Error("Transport.Base is nil; the safe transport was dropped")
	}
}

func TestWithTimeout_IgnoresNonPositive(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		var s settings
		WithTimeout(d)(&s)
		if s.timeout != 0 {
			t.Fatalf("WithTimeout(%v) set timeout to %v, want it left alone", d, s.timeout)
		}
	}
}

func TestWithTimeout_Applies(t *testing.T) {
	var s settings
	WithTimeout(3 * time.Second)(&s)
	if s.timeout != 3*time.Second {
		t.Fatalf("timeout = %v, want 3s", s.timeout)
	}
}
