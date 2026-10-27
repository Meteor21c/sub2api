# Meteor video relay

A small video-only service for Guangzhou. Korean Sub2API continues to authorize
tasks and settle billing. This service reads an existing video directly from its
storage host and streams it to the user; video bytes never pass through Korea.

## Delivery contract

- `GET` or `HEAD /v1/video/{token}` returns the media. `GET`/`HEAD /health` is public.
- Token: `v1.<iv>.<ciphertext>.<tag>`, canonical unpadded base64url parts.
- Encryption: AES-256-GCM, 12-byte random IV, 16-byte tag, AAD
  `meteor-video-relay:v1`. The shared key is 32 random bytes, encoded as exactly
  64 hex characters in `VIDEO_RELAY_SECRET` on each server.
- Encrypted JSON: `{ "v": 1, "aud": "meteor-video-relay", "id": "ASSET_ID",
  "src": "https://storage.example/video.mp4?temporary-signature=...",
  "iat": 1790000000, "exp": 1790000300 }`.
- `id` is 8–128 letters, digits, `_` or `-`. Lifetime is at most 3600 seconds;
  initial clock skew may be up to 30 seconds. Links remain bearer credentials:
  anyone holding one may read the video until it expires.
- Tokens are limited to 16384 characters and source URLs to 8192 characters.
  Neither source URLs, tokens, request headers, nor upstream error bodies are
  logged. HTTPS terminates at the Guangzhou Nginx virtual host.

The source URL is encrypted rather than merely signed or encoded. Responses do
not expose upstream redirects, cookies, filename, server headers, or errors.
Video images, watermarks, and embedded container metadata are preserved.

## Source and resource controls

Set `VIDEO_RELAY_ALLOWED_HOSTS` to exact HTTPS storage hosts. Every initial
request and redirect is checked again, including all DNS answers. Only public
unicast addresses are accepted. The HTTPS connection uses one validated IP
while retaining the original hostname for SNI and certificate verification.
No source Authorization, Cookie, Referer, or user-supplied request headers are
forwarded. Redirects remain inside the exact allowlist and never reach browsers.

The relay accepts a single byte Range, supports HEAD without downloading the
video, and streams with backpressure and cancellation on client disconnect.
Maximum video size is 256 MiB, default simultaneous transfers 8, connect/read
idle timeout 15 seconds, and total transfer lifetime 900 seconds. Tokens must be
valid when the request starts; a transfer already authorized may finish after
its token expires. DNS lookup is bounded by request cancellation and the total
transfer timeout. No video is
cached to disk. Responses use `private, no-store`; expiring tokens cannot be
bypassed through a shared cache. The original Korean content endpoint remains
available as an operational fallback.

## Deployment

Run `npm test` locally and build the image in GitHub Actions. Production servers
only pull prebuilt images. Put the Compose file and a mode-0600 `.env` in an
independent `/opt/meteor-video-relay` directory. Pin `VIDEO_RELAY_IMAGE` to its
verified digest. This service binds only `127.0.0.1:3103`; publish it through a
separate `media.meteor21c.fun` TLS virtual host. Preserve existing bot and download
site containers and virtual hosts. Do not log the media token path in Nginx.

```sh
docker compose pull meteor-video-relay
docker compose up -d --no-deps --force-recreate --no-build --pull never meteor-video-relay
```

Verify container and localhost health, HTTPS Range/206 behavior with an existing
completed video, and the browser's playable result. Invalid/expired tokens must
fail without revealing storage addresses. Keep the previous image and Compose
configuration for rollback; disabling relay issuance in Korea returns delivery
to the existing authenticated Korean content endpoint.
