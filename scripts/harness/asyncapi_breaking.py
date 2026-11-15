#!/usr/bin/env python3
"""AsyncAPI 2.x breaking-change gate for a producer's event contract.

Compares a BASE (base-branch) and HEAD (PR) asyncapi.yaml and fails on the
three changes that break consumers of a producer's events:

  1. a message (components.messages entry or channel message) is removed;
  2. a required payload data field is removed or renamed (a rename is a
     remove+add, so it shows up as a removal), or a required field becomes
     optional (consumers may no longer rely on its presence);
  3. type narrowing: a payload field's `type` shrinks (values the base
     schema accepted are now invalid) or an `enum` is added/shrunk.

Additive changes (new messages, new optional fields, new enum values,
widened types) are NOT breaking: consumers ignore what they do not know.
Schemas composed with allOf/$ref (the CloudEventEnvelope pattern) are
flattened before comparison.

Usage:
    python3 scripts/harness/asyncapi_breaking.py BASE_SPEC HEAD_SPEC   # exit 1 on breaking
    python3 scripts/harness/asyncapi_breaking.py --self-test          # built-in checks

Dependency: PyYAML for comparing files only (.github/requirements/chart-tests.txt
pins it; the spec-breaking CI job installs that file). --self-test needs none, so
the guide-lint job can run it on a bare runner.
"""
import sys

REF_PREFIX = "#/components/schemas/"


def flatten(schema, doc):
    """Resolve a local $ref and merge allOf members into one schema dict."""
    if not isinstance(schema, dict):
        return {}
    ref = schema.get("$ref")
    if isinstance(ref, str) and ref.startswith(REF_PREFIX):
        target = doc.get("components", {}).get("schemas", {}).get(ref[len(REF_PREFIX):])
        if target is not None:
            return flatten(target, doc)
    if "allOf" in schema:
        merged = {k: v for k, v in schema.items() if k != "allOf"}
        for part in schema["allOf"]:
            part = flatten(part, doc)
            merged["required"] = sorted(set(merged.get("required") or []) | set(part.get("required") or []))
            props = dict(merged.get("properties") or {})
            props.update(part.get("properties") or {})
            merged["properties"] = props
            for key in ("type", "enum"):
                if key in part and key not in merged:
                    merged[key] = part[key]
        return merged
    return schema


def collect_messages(doc):
    """-> {message name: flattened payload schema}, from components + channels."""
    out = {}
    for name, msg in (doc.get("components", {}).get("messages") or {}).items():
        out[name] = flatten(msg.get("payload") or {}, doc)
    for _channel, ch in (doc.get("channels") or {}).items():
        for _op, op in (ch or {}).items():
            if not isinstance(op, dict):
                continue
            msgs = op.get("message") or op.get("messages") or []
            msgs = [msgs] if isinstance(msgs, dict) else msgs
            for msg in msgs:
                if isinstance(msg, dict) and msg.get("name") and msg["name"] not in out:
                    out[msg["name"]] = flatten(msg.get("payload") or {}, doc)
    return out


def types_of(schema):
    """-> set of JSON Schema types, or None when untyped (accepts anything)."""
    t = schema.get("type")
    if t is None:
        return None
    out = set(t) if isinstance(t, list) else {t}
    if "number" in out:
        out.add("integer")  # every integer is a number: integer -> number is a widening
    return out


def field_breaks(base_schema, head_schema, doc_base, doc_head, path):
    """Breaking payload-field changes between two flattened schemas."""
    out = []
    base_req = set(base_schema.get("required") or [])
    head_req = set(head_schema.get("required") or [])
    base_fields, head_fields = base_schema.get("properties") or {}, head_schema.get("properties") or {}
    for field in sorted(base_req):
        if field not in head_fields:
            out.append(f"{path}.{field}: required data field removed (or renamed)")
        elif field not in head_req:
            out.append(f"{path}.{field}: required data field became optional")
    for field, base_field in sorted(base_fields.items()):
        head_field = flatten(head_fields.get(field) or {}, doc_head)
        base_field = flatten(base_field, doc_base)
        where = f"{path}.{field}"
        base_types, head_types = types_of(base_field), types_of(head_field)
        if base_types is not None and head_types is not None and not base_types <= head_types:
            out.append(f"{where}: type narrowed {sorted(base_types)} -> {sorted(head_types)}")
        if "enum" in head_field:
            if "enum" not in base_field:
                out.append(f"{where}: enum added (type narrowed to a fixed set)")
            elif not set(base_field["enum"]) <= set(head_field["enum"]):
                out.append(f"{where}: enum values removed")
        out.extend(field_breaks(base_field, head_field, doc_base, doc_head, where))
    return out


def breaking_changes(base_doc, head_doc):
    """-> list of human-readable breaking changes between two AsyncAPI docs."""
    out = []
    base_msgs, head_msgs = collect_messages(base_doc), collect_messages(head_doc)
    for name in sorted(base_msgs):
        if name not in head_msgs:
            out.append(f"message {name}: removed")
        else:
            out.extend(field_breaks(base_msgs[name], head_msgs[name], base_doc, head_doc, f"message {name} payload"))
    return out


def _self_test():
    envelope = {"type": "object", "required": ["specversion"],
                "properties": {"specversion": {"type": "string"}}}
    base = {"components": {
        "schemas": {"Envelope": envelope,
                    "P": {"allOf": [{"$ref": "#/components/schemas/Envelope"},
                                    {"type": "object",
                                     "properties": {"data": {"type": "object",
                                                             "required": ["sku", "qty"],
                                                             "properties": {"sku": {"type": "string"},
                                                                            "qty": {"type": ["integer", "string"]},
                                                                            "tag": {"enum": ["a", "b"]}}}}}]},
                    "GoneSchema": {"type": "object"}},
        "messages": {"Kept": {"payload": {"$ref": "#/components/schemas/P"}},
                     "Gone": {"payload": {"$ref": "#/components/schemas/GoneSchema"}}}}}
    head = {"components": {
        "schemas": {"Envelope": envelope,
                    "P": {"allOf": [{"$ref": "#/components/schemas/Envelope"},
                                    {"type": "object",
                                     "properties": {"data": {"type": "object",
                                                             "required": ["sku", "qty", "extra"],
                                                             "properties": {"sku": {"type": "string"},
                                                                            "qty": {"type": "integer"},
                                                                            "tag": {"enum": ["a", "b", "c"]},
                                                                            "extra": {"type": "string"}}}}}]},
                    "AddedSchema": {"type": "object"}},
        "messages": {"Kept": {"payload": {"$ref": "#/components/schemas/P"}},
                     "Added": {"payload": {"$ref": "#/components/schemas/AddedSchema"}}}}}
    found = breaking_changes(base, head)
    expected = ["message Gone: removed",
                "message Kept payload.data.qty: type narrowed ['integer', 'string'] -> ['integer']"]
    for exp in expected:
        assert exp in found, f"expected finding missing: {exp!r}\ngot: {found}"
    additions = [f for f in found if "extra" in f or "Added" in f or "c" in f]
    assert not additions, f"additive change flagged as breaking: {additions}"
    assert breaking_changes(base, base) == [], "identical specs flagged as breaking"
    widened = {"components": {"schemas": {"S": {"type": "object", "properties": {"n": {"type": "integer"}}}},
                              "messages": {"M": {"payload": {"$ref": "#/components/schemas/S"}}}}}
    wider = {"components": {"schemas": {"S": {"type": "object", "properties": {"n": {"type": "number"}}}},
                            "messages": {"M": {"payload": {"$ref": "#/components/schemas/S"}}}}}
    assert breaking_changes(widened, wider) == [], "integer -> number widening flagged as breaking"
    assert breaking_changes(wider, widened), "number -> integer narrowing not flagged"
    # a required data field removed / renamed, a required field turned optional,
    # an enum introduced on an open field
    gutted = {"components": {
        "schemas": {"Envelope": envelope,
                    "P": {"allOf": [{"$ref": "#/components/schemas/Envelope"},
                                    {"type": "object",
                                     "properties": {"data": {"type": "object",
                                                             "required": ["sku"],
                                                             "properties": {"sku": {"type": "string"},
                                                                            "quantity": {"type": "integer"},
                                                                            "tag": {"enum": ["a"]}}}}}]},
                    "GoneSchema": {"type": "object"}},
        "messages": {"Kept": {"payload": {"$ref": "#/components/schemas/P"}},
                     "Gone": {"payload": {"$ref": "#/components/schemas/GoneSchema"}}}}}
    gutted_found = breaking_changes(base, gutted)
    for exp in ("message Kept payload.data.qty: required data field removed (or renamed)",
                "message Kept payload.data.tag: enum values removed"):
        assert exp in gutted_found, f"expected finding missing: {exp!r}\ngot: {gutted_found}"
    print("asyncapi_breaking self-test: OK (%d+%d breaking findings, additives not flagged)"
          % (len(found), len(gutted_found)))


def main(argv):
    if argv == ["--self-test"]:
        _self_test()
        return 0
    if len(argv) != 2:
        print("usage: asyncapi_breaking.py BASE_SPEC HEAD_SPEC | --self-test", file=sys.stderr)
        return 2
    import yaml  # deferred: --self-test and the dict API need no PyYAML

    with open(argv[0], encoding="utf-8") as fh:
        base_doc = yaml.safe_load(fh)
    with open(argv[1], encoding="utf-8") as fh:
        head_doc = yaml.safe_load(fh)
    findings = breaking_changes(base_doc, head_doc)
    for line in findings:
        print(f"BREAKING: {line}")
    print(f"asyncapi breaking changes: {len(findings)}")
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
