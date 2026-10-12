// NOTE: goja reads toLocaleString(locale)'s argument as a radix and throws RangeError, so digits are grouped by hand.
function group(digits) {
  return String(digits).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

function num(n) {
  const v = Number(n);
  if (!isFinite(v)) return "0";
  const neg = v < 0;
  return (neg ? "-" : "") + group(Math.abs(v));
}

function day(ts) {
  if (!ts) return "—";
  const s = String(ts);
  const t = s.indexOf("T");
  return t > 0 ? s.slice(0, t) : s;
}

// SECURITY: a view model carries only strings this bundle minted, so a duck-typed tool result cannot smuggle a marker past the render layer.
function text(value, fallback) {
  if (value === undefined || value === null || value === "") return fallback;
  if (typeof value === "object") return fallback;
  return String(value);
}
