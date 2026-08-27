r"""The JSON Schema subset tool arguments are validated against (B10).

Deliberately hand-written and small rather than a `jsonschema`
dependency: the schemas in `tools\builtins\` are four flat objects of
strings and integers, and B10's Build section adds no dependency without
a reason in the handoff. What is supported is exactly what the built-in
tools use:

    type: object / string / integer / number / boolean / array
    properties, required, enum
    minimum / maximum (numbers), default

Anything beyond that is *ignored*, not rejected -- an unsupported keyword
must never make a valid call look invalid. Unknown properties on the
instance are likewise allowed through: JSON Schema's own default, and the
thing that lets `faults.py` ride a `__mock_fault` key into a call without
every schema declaring it.

Returns a list of human-readable problems, empty when the arguments are
acceptable. The strings go straight to the model inside a
ToolResult(ok=False), so they are written to be read by one.
"""

from __future__ import annotations

from typing import Any

_TYPE_CHECKS = {
    "object": lambda v: isinstance(v, dict),
    "array": lambda v: isinstance(v, list),
    "string": lambda v: isinstance(v, str),
    "integer": lambda v: isinstance(v, int) and not isinstance(v, bool),
    "number": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
    "boolean": lambda v: isinstance(v, bool),
    "null": lambda v: v is None,
}


def validate(instance: Any, schema: dict[str, Any], path: str = "arguments") -> list[str]:
    problems: list[str] = []

    expected = schema.get("type")
    if expected is not None:
        check = _TYPE_CHECKS.get(expected)
        if check is not None and not check(instance):
            return [f"{path} must be of type {expected}, got {_name_of(instance)}"]

    if "enum" in schema and instance not in schema["enum"]:
        allowed = ", ".join(repr(v) for v in schema["enum"])
        problems.append(f"{path} must be one of: {allowed}")

    if isinstance(instance, (int, float)) and not isinstance(instance, bool):
        minimum = schema.get("minimum")
        maximum = schema.get("maximum")
        if minimum is not None and instance < minimum:
            problems.append(f"{path} must be >= {minimum}")
        if maximum is not None and instance > maximum:
            problems.append(f"{path} must be <= {maximum}")

    if isinstance(instance, dict):
        for name in schema.get("required", []):
            if name not in instance:
                problems.append(f"{path} is missing required property {name!r}")
        for name, subschema in (schema.get("properties") or {}).items():
            if name in instance:
                problems.extend(validate(instance[name], subschema, f"{path}.{name}"))

    if isinstance(instance, list) and isinstance(schema.get("items"), dict):
        for index, item in enumerate(instance):
            problems.extend(validate(item, schema["items"], f"{path}[{index}]"))

    return problems


def apply_defaults(arguments: dict[str, Any], schema: dict[str, Any]) -> dict[str, Any]:
    """Fill in `default`s the model omitted, so a tool body never has to
    repeat its own schema's defaults in Python and drift from them."""
    filled = dict(arguments)
    for name, subschema in (schema.get("properties") or {}).items():
        if name not in filled and isinstance(subschema, dict) and "default" in subschema:
            filled[name] = subschema["default"]
    return filled


def _name_of(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, str):
        return "string"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        return "number"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    return type(value).__name__
