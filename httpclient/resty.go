package httpclient

import (
	"cmp"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
)

// Defaults applied by NewResty when RestyOptions leaves a field zero.
const (
	DefaultRestyTimeout = 10 * time.Second
	DefaultUserAgent    = "altempl/1"
)

// RestyOptions configures NewResty; the zero value yields safe defaults.
type RestyOptions struct {
	Timeout           time.Duration
	UserAgent         string
	HTTPClient        *http.Client
	AllowRedirects    bool
	AllowPrivateHosts bool
	ResponseBodyLimit int
	Retry             RetryPolicy
}

// NewResty returns a *resty.Client on the safe transport, refusing redirects unless AllowRedirects is set.
func NewResty(opts RestyOptions) *resty.Client {
	httpc := opts.HTTPClient
	if httpc == nil {
		// NOTE: no WithTimeout here — SetTimeout below overwrites http.Client.Timeout either way.
		httpc = New(WithAllowPrivateHosts(opts.AllowPrivateHosts))
	}
	rc := resty.NewWithClient(httpc).
		SetTimeout(cmp.Or(opts.Timeout, DefaultRestyTimeout)).
		SetHeader("User-Agent", cmp.Or(opts.UserAgent, DefaultUserAgent))
	// NOTE: a caller-supplied client may already retry in its transport; a second ladder here would multiply attempts.
	if opts.Retry.enabled() && !hasRetryTransport(httpc) {
		applyRestyRetry(rc, opts.Retry.withDefaults())
	}
	// SECURITY: a 307/308 from a token endpoint would replay client_secret to the redirect target.
	if !opts.AllowRedirects {
		rc.SetRedirectPolicy(resty.NoRedirectPolicy())
	}
	if opts.ResponseBodyLimit > 0 {
		rc.SetResponseBodyLimit(opts.ResponseBodyLimit)
	}
	return rc
}
