const BADGE_FALLBACK = "#64748b";

// SECURITY: the result lands in a raw style="background:…" attribute, so only an allow-listed token may leave this function.
const HEX_COLOUR = /^#[0-9a-fA-F]{6}$/;
const BADGE_TOKEN = /^[a-z][a-z0-9-]{0,23}$/;

function badgeColour(token) {
  const t = String(token === undefined || token === null ? "" : token);
  if (HEX_COLOUR.test(t)) return t;
  if (BADGE_TOKEN.test(t)) return "var(--color-badge-" + t + ", " + BADGE_FALLBACK + ")";
  return BADGE_FALLBACK;
}
