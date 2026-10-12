# Webhooks

The receiver contract for outbound webhooks (S5 dispatch). This is a **wire contract**: the
envelope, the headers and the signature hold for every tenant endpoint. Adding an event:
[`howto/webhook-out.md`](../howto/webhook-out.md). Transport and signing rule R10:
[`dispatch and cli`](../surfaces/dispatch-and-cli.md#r10--dispatch-in-full).

## Endpoints

| Rule          | Value                                                                                                        |
| ------------- | ------------------------------------------------------------------------------------------------------------ |
| Scope         | one project; the console under `/orgs/{org}/projects/{project}/webhooks`                                     |
| URL           | checked at create/update: `https://` only, ≤ 2048 bytes, no userinfo, a host                                 |
| Dial          | delivery connects only to public addresses; a private target is accepted at create, then fails each delivery |
| Limit         | 10 per project (`webhook.MaxEndpointsPerProject`, `WHK004`)                                                  |
| Subscriptions | at least one subscribable type, no duplicates                                                                |
| Inactive      | receives nothing new; queued deliveries retry, then fail                                                     |
| Secret        | `whsec_` + 43 base64url chars (32 random bytes); shown once, at create/rotate                                |

## Envelope

Every delivery is one JSON object. The bytes are built once, at enqueue; every attempt sends the
same body.

| Field                 | Type                  | Meaning                                                        |
| --------------------- | --------------------- | -------------------------------------------------------------- |
| `id`                  | string                | `evt_` + UUIDv7. One event fanned out to N endpoints shares it |
| `type`                | string                | the event type, from the catalog below                         |
| `api_version`         | string                | the payload version, `"v1"`                                    |
| `created_at`          | RFC 3339 UTC, seconds | when the event was enqueued                                    |
| `tenant.org_id`       | uuid                  | the org                                                        |
| `tenant.project_id`   | uuid                  | the project the endpoint belongs to                            |
| `tenant.project_slug` | string                | the project slug **at enqueue time**                           |
| `data`                | object                | the event payload                                              |

Any timestamp inside `data` (`first_published_at`, `updated_at`) is RFC 3339 UTC, whole seconds — same precision as `created_at`.

```json
{
  "id": "evt_0199c1f0-0000-7000-8000-000000000001",
  "type": "blog.post.published",
  "api_version": "v1",
  "created_at": "2026-09-27T04:11:00Z",
  "tenant": {
    "org_id": "0199c1f0-0000-7000-8000-00000000000a",
    "project_id": "0199c1f0-0000-7000-8000-00000000000b",
    "project_slug": "altalune"
  },
  "data": {
    "id": "0199c1f0-0000-7000-8000-00000000000c",
    "slug": "hello-world",
    "title": "Hello, world",
    "body_markdown": "# Hello",
    "category_id": "0199c1f0-0000-7000-8000-00000000000d",
    "tag_ids": ["0199c1f0-0000-7000-8000-00000000000e"],
    "first_published_at": "2026-09-27T04:00:00Z",
    "updated_at": "2026-09-27T04:10:00Z",
    "version": 3
  }
}
```

## Events

Source of truth: `internal/platform/events` (`catalog.go`, `payloads.go`, golden files in `testdata/`).

| Type                    | Subscribable     | Fires when                               | `data`              |
| ----------------------- | ---------------- | ---------------------------------------- | ------------------- |
| `blog.post.published`   | yes              | a draft becomes published                | `PostPublishedV1`   |
| `blog.post.unpublished` | yes              | a published post becomes a draft         | `PostUnpublishedV1` |
| `blog.post.deleted`     | yes              | a post is deleted, draft or published    | `PostDeletedV1`     |
| `webhook.ping`          | no — "Send test" | the console sends a test to one endpoint | `WebhookPingV1`     |

- A no-op publish or unpublish sends nothing. An edit sends nothing: v1 has no `updated` event.
- `published` and `unpublished` share one shape (the example above). `tag_ids` is always an
  array, never `null`. `version` is the stored post version after the change.

| Event               | Example `data`                                                                                 |
| ------------------- | ---------------------------------------------------------------------------------------------- |
| `blog.post.deleted` | `{"id": "018f9c3e-1111-7000-8000-000000000001", "slug": "hello-world", "was_published": true}` |
| `webhook.ping`      | `{"endpoint_id": "018f9c3e-4444-7000-8000-000000000004"}`                                      |

## Headers

| Header                    | Value                                                                |
| ------------------------- | -------------------------------------------------------------------- |
| `Content-Type`            | `application/json`                                                   |
| `User-Agent`              | `Opensheet-Webhooks/1`                                               |
| `X-Opensheet-Event-Id`    | `evt_<uuid>`, equal to the envelope `id`                             |
| `X-Opensheet-Event-Type`  | the envelope `type`                                                  |
| `X-Opensheet-Delivery-Id` | `dlv_<uuid>`, one per event per endpoint, stable across retries      |
| `X-Opensheet-Timestamp`   | decimal Unix seconds of **this attempt**                             |
| `X-Opensheet-Signature`   | `v1=<hex>`, or `v1=<hex-primary> v1=<hex-secondary>` during rotation |

## Signature

1. Take the secret string exactly as shown, `whsec_` prefix included, as UTF-8 bytes. No decoding.
2. Compute `HMAC-SHA256(secret, timestamp + "." + raw_body)`, where `timestamp` is the
   `X-Opensheet-Timestamp` string and `raw_body` is the bytes received, before any JSON parsing.
3. Render lowercase hex and prefix `v1=`.
4. Accept when any space-separated value in `X-Opensheet-Signature` matches, compared in constant time.
5. Reject a timestamp more than 5 minutes from your clock, either way.

Test vector: secret `whsec_test`, timestamp `1758153600`, body the exact bytes `{"a":1}` →
`v1=5d7a59cb9a5399806655f45f56c0139fbffdf766435435b0d7004e9d9ae115b1`.

Go:

```go
func Verify(secret string, h http.Header, body []byte, now time.Time) bool {
	ts := h.Get("X-Opensheet-Timestamp")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := now.Sub(time.Unix(sec, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := []byte("v1=" + hex.EncodeToString(mac.Sum(nil)))
	for _, got := range strings.Fields(h.Get("X-Opensheet-Signature")) {
		if hmac.Equal([]byte(got), want) {
			return true
		}
	}
	return false
}
```

Node (`rawBody` is a `Buffer` of the unparsed body, e.g. from `express.raw()`):

```js
import crypto from "node:crypto";

export function verify(secret, headers, rawBody, nowMs = Date.now()) {
  const ts = headers["x-opensheet-timestamp"] ?? "";
  if (!/^\d+$/.test(ts) || Math.abs(nowMs / 1000 - Number(ts)) > 300)
    return false;
  const mac = crypto
    .createHmac("sha256", secret)
    .update(`${ts}.`)
    .update(rawBody);
  const want = Buffer.from(`v1=${mac.digest("hex")}`);
  return (headers["x-opensheet-signature"] ?? "").split(" ").some((sig) => {
    const got = Buffer.from(sig);
    return got.length === want.length && crypto.timingSafeEqual(got, want);
  });
}
```

## Delivery semantics

- **At least once.** The same delivery can arrive twice (a lost 2xx, a worker lease expiry).
- **Dedupe on `X-Opensheet-Delivery-Id`.** Correlate on `X-Opensheet-Event-Id`: two endpoints get one
  event id and two delivery ids.
- **Success is a 2xx only.** A 3xx is a failure: redirects are never followed. So is a timeout
  (10s) or a refused connection. The response body is ignored.
- Answer 2xx fast, then do the work. No ordering is promised between deliveries.

## Retries

8 attempts over about 28h. Each wait is jittered ±10%.

| Before attempt | 1   | 2   | 3   | 4   | 5   | 6   | 7   | 8   |
| -------------- | --- | --- | --- | --- | --- | --- | --- | --- |
| Wait           | —   | 30s | 5m  | 30m | 2h  | 5h  | 10h | 10h |

After attempt 8 the delivery is `failed` and stays in the console. Source: `outbox.MaxAttempts`, `outbox.Backoff`.

## Console retry

- **Retry** requeues one `failed` delivery: attempt 0, due now, same delivery id and body.
- **Retry all failed (N)** does the same for every failed delivery of one endpoint.
- A `pending` or `delivered` delivery cannot be retried (`WHK005`): no race with the automatic retry.
- A retried delivery reuses its delivery id: if you already processed it, answer 2xx.
- Each delivery's row shows the exact body sent and its headers; the timestamp and signature are per attempt and not stored.
- Each attempt keeps your response: the first 4 KiB of the body (`webhook.MaxResponseBodyBytes`) and its headers, minus cookies and credential-like headers.

## Rotation

1. **Rotate** mints a new primary, shown once. The old primary becomes the secondary.
2. Every delivery carries both signatures, primary first. Deploy the new secret at your own pace.
3. **Retire** drops the secondary. Nothing retires it for you.

- Rotating again during a rotation drops the old secondary; the current primary becomes the secondary.
- A rotate or retire that races another secret change is refused with `WHK008`: reload and retry.
- If the server's encryption key was ephemeral and the process restarted, secrets cannot be opened:
  deliveries fail with `webhook: signing secret unavailable: rotate the endpoint secret to recover` until you rotate. See [`config`](../config/README.md#encryption-at-rest).

## Versioning

- Within v1, changes are additive only: a new field you may ignore. Ignore unknown fields.
- Renaming, removing or retyping a field, or changing its meaning, needs a new payload version
  (`...V2`, `api_version: "v2"`).
- A v2 ships only with a per-endpoint version selector in the console. Until then, one version per event.
- A new event type reaches you only if you subscribe to it.

Error codes `WHK001`–`WHK008`: [`error codes`](../errors/README.md#whk--webhooks).
