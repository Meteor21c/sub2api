import { createHash, timingSafeEqual } from "node:crypto";

// Hash both inputs before comparison to keep the comparison fixed in length.
// An empty expected token always fails closed, including "Bearer ".
export function snapshotAuthorized(authorization, expectedToken) {
  if (typeof expectedToken !== "string" || !expectedToken.trim()) return false;
  if (typeof authorization !== "string" || authorization.length > 4096) return false;
  const match = authorization.match(/^Bearer ([^\s]+)$/i);
  if (!match) return false;
  const digest = (value) => createHash("sha256").update(value).digest();
  return timingSafeEqual(digest(match[1]), digest(expectedToken.trim()));
}
