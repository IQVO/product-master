"""Schemathesis hooks for scripts/contract-test.sh (loaded via SCHEMATHESIS_HOOKS).

Each hook rewrites generated data the server rejects for a reason
apis/openapi.yaml does NOT (or cannot) encode, so the positive_data_acceptance
check stays ON for everything else. They are maps, not filters: a filter over
random unicode discards most candidates and makes the run far slower. Every entry
names why it is here and what retires it.

* measured-at  — `measuredAt` must not be in the future (a clock-dependent rule no
  schema expresses; format: date-time only) and the zero time reads as missing.
* control-chars — free-text body fields (description, deviceId, reason): the code
  rejects control characters (product.ErrInvalidDescription / ErrInvalidReason /
  the device-id rule); the spec only documents length. Encoding it means adding a
  `pattern` to previously unrestricted request properties, which oasdiff classifies
  as breaking (request-property-pattern-added), so it is deferred to a coordinated
  change rather than slipped into the gate PR.
* idempotency  — Idempotency-Key: the description says 1 to 128 printable ASCII
  characters, the schema only bounds the length; same oasdiff classification
  (request-parameter-pattern-added) and same deferral.
"""
from datetime import datetime, timezone
from urllib.parse import quote, unquote

import schemathesis

CLEAN = "x"


def _printable_ascii(value):
    return "".join(c if 0x20 <= ord(c) <= 0x7E else CLEAN for c in value)


def _no_control(value):
    return "".join(CLEAN if ord(c) < 0x20 or 0x7F <= ord(c) <= 0x9F else c for c in value)


def _walk(value):
    """Rewrite every string of a JSON body: control characters -> CLEAN."""
    if isinstance(value, str):
        return _no_control(value)
    if isinstance(value, dict):
        return {key: _walk(item) for key, item in value.items()}
    if isinstance(value, list):
        return [_walk(item) for item in value]
    return value


@schemathesis.hook
def map_path_parameters(context, path_parameters):
    sku = path_parameters.get("sku") if isinstance(path_parameters, dict) else None
    if not isinstance(sku, str):
        return path_parameters
    # Path parameters arrive already percent-encoded: decode, clean, re-encode.
    # A literal '%' is NOT cleaned: it is a valid SKU character and the handler
    # decodes the segment exactly once (routedParam; the former sku-percent hook
    # worked around a double decode and was retired with that fix).
    #  - sku-space: the spec's `\s` is ECMA-262 (Unicode) whitespace, but the
    #    generator also emits NBSP and friends; the code rejects them all
    #    (unicode.IsSpace), the spec text agrees, only the generator disagrees.
    raw = unquote(sku)
    clean = "".join(CLEAN if c.isspace() else c for c in raw)
    if clean != raw:
        path_parameters["sku"] = quote(clean, safe="")
    return path_parameters


@schemathesis.hook
def map_headers(context, headers):
    key = headers.get("Idempotency-Key") if isinstance(headers, dict) else None
    if isinstance(key, str):
        # An HTTP layer trims leading/trailing blanks, which would make the sent
        # key shorter (possibly empty) than the generated one.
        headers["Idempotency-Key"] = _printable_ascii(key).strip() or CLEAN
    return headers


@schemathesis.hook
def map_body(context, body):
    body = _walk(body)  # control-chars
    measured_at = body.get("measuredAt") if isinstance(body, dict) else None
    if isinstance(measured_at, str):  # measured-at
        try:
            when = datetime.fromisoformat(measured_at.replace("Z", "+00:00"))
        except ValueError:
            return body
        if when.tzinfo is None:
            when = when.replace(tzinfo=timezone.utc)
        # Also the zero/ancient time: the handler reads year 1 as "missing".
        if when > datetime.now(timezone.utc) or when.year < 1970:
            body["measuredAt"] = "2026-01-01T00:00:00Z"
    return body
