package handlers

import (
	"net/http"

	"github.com/google/uuid"
)

// WebhookErrorRuleOf returns the key, refusal flag and max of the rule matching err.
func WebhookErrorRuleOf(err error) (key string, refusal bool, maxLen int) {
	r := lookupWebhookError(err)
	return r.key, r.refusal, r.max
}

// WebhookErrorRuleCount returns how many rules the webhook error table holds.
func WebhookErrorRuleCount() int { return len(webhookErrorRules()) }

// IsSignInRefusal reports whether a sign-in failure answers the 403 refusal page rather than the 500 unavailable page.
func IsSignInRefusal(err error) bool { return isRefusal(err) }

// OrgScopeRequest returns the request an OrgScope carries.
func OrgScopeRequest(sc OrgScope) *http.Request { return sc.req }

// OrgScopeOrgID returns the org an OrgScope acts on.
func OrgScopeOrgID(sc OrgScope) uuid.UUID { return sc.org.ID }

// ProjectScopeRequest returns the request a ProjectScope carries.
func ProjectScopeRequest(sc ProjectScope) *http.Request { return sc.req }
