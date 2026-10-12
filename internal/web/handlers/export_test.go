package handlers

// WebhookErrorRuleOf returns the key, refusal flag and max of the rule matching err.
func WebhookErrorRuleOf(err error) (key string, refusal bool, maxLen int) {
	r := lookupWebhookError(err)
	return r.key, r.refusal, r.max
}

// WebhookErrorRuleCount returns how many rules the webhook error table holds.
func WebhookErrorRuleCount() int { return len(webhookErrorRules()) }
