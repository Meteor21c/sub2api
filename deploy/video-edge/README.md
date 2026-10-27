# Private video delivery on EdgeOne

Traffic follows `browser → media.meteor21c.fun → EdgeOne edge function → video storage`.
The Korean bridge keeps task authorization and billing, and signs the existing
AES-256-GCM capability contract. The storage URL and its provider signature are
encrypted. The edge function validates the capability and internally fetches
the original signed storage URL. It never redirects the browser to storage.

## Platform setup

1. Use **Global availability (excluding the Chinese mainland)** for the site.
   This region supports domains without ICP filing. Select **CNAME access** to
   retain Aliyun DNS. Verify ownership and obtain the EdgeOne-issued CNAME.
2. Add `media.meteor21c.fun` and configure an independently valid TLS certificate.
   Replace the previous Guangzhou A record with the assigned CNAME only after
   the site and certificate are ready. Do not change other domain records.
3. Create an edge function and set these function environment values:
   - **Secret** `VIDEO_RELAY_SECRET`: the same independent 32-byte key (64 hex
     characters) used by the bridge. Do not use the asset-signing secret.
   - **String** `VIDEO_RELAY_ALLOWED_HOSTS`: comma-separated, exact public video
     storage DNS names obtained from existing successful video metadata. Do not
     allow wildcards, arbitrary hosts, IP addresses or metadata-service names.
   - Optional **String** `VIDEO_RELAY_CACHE_ENABLED`: `true` (default) or `false`.
     Caching is available only when the account/runtime supports the Cache API.
4. Run locally: `node deploy/video-edge/render.mjs /absolute/new/path/edge.js`.
   This creates a mode-0600 standalone script without any private configuration.
   Paste that script into EdgeOne. No server compilation is needed.
5. Trigger this function for all paths on the media hostname. The function
   handles only `GET/HEAD /health` and `GET/HEAD /v1/video/{encrypted-token}`.
   Unsupported methods and paths fail closed. Missing environment configuration
   returns 503, including for health requests.
   Disable ordinary route/CDN caching for **all external media paths**. Every
   external request must execute the function before any cached data is served.
   Do not add a rule that caches the encrypted-capability response itself.
6. Disable request-URL/path/query logging or redact the capability path. Do not
   enable function debug logging that prints requests, tokens, provider URLs or
   response bodies. Limit function access and request volume in the platform.
7. Before setting the bridge's `VIDEO_RELAY_BASE_URL=https://media.meteor21c.fun`,
   verify public TLS, health, an existing video with HEAD and Range/206, neutral
   headers, and actual playback. Keep the Korean fallback available. Never
   generate another paid video just to validate delivery.

The bridge also needs matching `VIDEO_RELAY_SECRET`, `VIDEO_RELAY_ALLOWED_HOSTS`
and `VIDEO_RELAY_TTL_SECONDS=1800`. Omit `VIDEO_RELAY_BASE_URL` to restore Korean
delivery. Historical signed Korean task URLs remain unchanged in storage.

## Security and delivery behavior

- Capabilities are authenticated with AES-256-GCM, a fixed audience/AAD, a
  maximum one-hour lifetime and a 30-second future clock allowance. The default
  bridge lifetime is 30 minutes. Anyone holding the complete URL can read that
  video until it expires. Do not disclose or log full URLs.
- Every storage URL and every redirect must use HTTPS/443 and an exact allowed
  public DNS name. At most three redirects are followed internally. The edge
  runtime does not provide the relay's DNS lookup/socket pinning APIs: the
  operator must allow only known public storage hosts they trust. A client
  cannot choose another URL without the signing key.
- Authorization, Cookie and Referer are not sent upstream. Responses contain
  only validated video headers and a neutral filename. Upstream Location,
  Server, vendor headers, cookies and error bodies are discarded.
- Single Range, 206, 416 and HEAD are supported. HEAD first uses native HEAD.
  If signed storage rejects it with 403/405/501, the function probes
  `GET Range: bytes=0-0`, derives full size from validated response headers and
  immediately cancels the body. A range-ignoring 200 is also cancelled, with a
  validated full Content-Length required. It never reads the complete video to
  answer HEAD. Client HEAD Range headers are ignored. Bodies stream with
  cancellation and a 256 MiB cap.
  No `arrayBuffer`, `blob` or whole-video buffering is used. Fetch connect/write
  timeouts are 15 seconds and the read timeout is 300 seconds, the documented
  EdgeOne maximum. Platform execution/transfer limits still apply.
- Every browser response uses **`private, no-store`**. The optional explicit
  Cache API stores complete 200 GETs only when a validated Content-Length is
  present and is at most **16 MiB**. Cache lifetime is at most **300 seconds**
  and never exceeds the current token's remaining lifetime. The token and
  allowlist are validated **before every cache lookup**, including warm Range
  reads. Internal keys hash the video ID and the complete signed storage URL;
  the internal key path is rejected for external callers. Stored headers are
  sanitized and use a separate internal public/max-age directive. Returned
  cached responses are sanitized again and remain private/no-store. An internal
  absolute expiry is also checked, so slow streamed cache writes cannot extend
  the usable lifetime even if provider max-age starts after the write finishes.
- Cold 206, HEAD, unknown-sized and >16 MiB videos are not written to cache.
  Warm Range requests use Cache API's documented Range/206 behavior. A cache
  read/write failure falls back to normal streaming. Cache population uses
  `event.waitUntil`; its clone is bounded by the enforced <=16 MiB body length
  and may finish filling after a client disconnects. Cache reuse is local to
  the provider's nodes and may be evicted; it is not permanent video storage.
- A fetch to a different storage hostname is an EdgeOne public-network
  subrequest, **not an automatic hit on ordinary site CDN caching**. Only the
  explicit authenticated Cache API logic above provides reuse. Measure actual
  Chinese-mainland playback speed and account cache availability before
  claiming a production improvement.
- Account plan/function quotas, traffic charges, log redaction, public TLS and
  production performance must be checked in the actual EdgeOne account.

## Verification

`node --test deploy/video-edge/test/*.test.mjs`

Tests include interoperability with the real Korean bridge signer, corrupted
and expired capabilities, request and response header isolation, redirects,
Range/HEAD behavior, streaming/cancellation, configuration failure and the
standalone platform wrapper. Cache tests cover expiry before hits, warm Range,
short TTLs, sanitized internal entries, skip conditions and cache failures.
They do not replace a live edge deployment test.

## Official references

- [CNAME and acceleration region](https://cloud.tencent.com/document/product/1552/87601)
- [Environment variables and Secret](https://edgeone.ai/document/62764)
- [Fetch and timeout configuration](https://edgeone.ai/document/52687)
- [Cache API and Range behavior](https://edgeone.ai/document/52684)
- [Web Crypto](https://cloud.tencent.com/document/product/1552/83933)
- [Response body limits](https://cloud.tencent.com/document/product/1552/81917)
