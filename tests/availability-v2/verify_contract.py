#!/usr/bin/env python3
"""Offline Availability V2 contract and baseline-evidence verifier.

This module deliberately uses only the Python standard library.  It never opens
a socket, invokes a subprocess, or calls an HTTP client.  By default it checks
an embedded wire fixture and the source evidence documented in
docs/availability-v2/QA_AUDIT.md.  That is a contract check, not an integration
claim.  ``--require-v2`` is the strict gate: it requires implementation source
evidence and an externally supplied, sanitized fixture.

External fixture shape::

    {
      "responses": [
        {"name": "catalog", "envelope": {"code": 0, ...}},
        {"name": "conversation_create", "envelope": {"code": 0, ...}},
        {"name": "conversation_list", "envelope": {"code": 0, ...}},
        {"name": "conversation_detail", "envelope": {"code": 0, ...}}
      ],
      "sse_streams": {"success": "event: ...\\ndata: {...}\\n\\n"}
    }

The fixture is wire-shaped on purpose: SSE must be represented as ``data:``
lines so JSON parsing and event framing are exercised rather than bypassed.
"""

from __future__ import annotations

import argparse
import json
import math
import re
import sys
from datetime import datetime
from pathlib import Path
from typing import Any


EVENT_TYPES = {
    "turn_started",
    "routing",
    "attempt_started",
    "attempt_failed",
    "content_delta",
    "turn_completed",
    "turn_failed",
    "turn_cancelled",
}
TERMINAL_EVENT_STATUS = {
    "turn_completed": "succeeded",
    "turn_failed": "failed",
    "turn_cancelled": "cancelled",
}
ATTEMPT_STATUSES = {"running", "succeeded", "failed", "cancelled"}
TURN_STATUSES = {"running", "succeeded", "failed", "cancelled"}
UPSTREAM_SUPPORT = {"declared", "unknown", "unsupported"}
RFC3339 = re.compile(
    r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}"
    r"(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$"
)


class ContractError(AssertionError):
    """Raised for a precise contract violation."""


def fail(message: str) -> None:
    raise ContractError(message)


def require(condition: bool, message: str) -> None:
    if not condition:
        fail(message)


def require_dict(value: Any, label: str) -> dict[str, Any]:
    require(isinstance(value, dict), f"{label} must be an object")
    return value


def require_list(value: Any, label: str) -> list[Any]:
    require(isinstance(value, list), f"{label} must be an array")
    return value


def require_string(value: Any, label: str, non_empty: bool = True) -> str:
    require(isinstance(value, str), f"{label} must be a string")
    if non_empty:
        require(bool(value.strip()), f"{label} must not be empty")
    return value


def require_int(value: Any, label: str, minimum: int | None = None) -> int:
    require(isinstance(value, int) and not isinstance(value, bool), f"{label} must be an integer")
    if minimum is not None:
        require(value >= minimum, f"{label} must be >= {minimum}")
    return value


def require_id(value: Any, label: str) -> int:
    return require_int(value, label, 1)


def require_number_or_null(value: Any, label: str) -> None:
    if value is None:
        return
    require(
        isinstance(value, (int, float)) and not isinstance(value, bool),
        f"{label} must be a number or null",
    )
    require(math.isfinite(float(value)), f"{label} must be finite")


def require_duration(value: Any, label: str) -> None:
    require_number_or_null(value, label)
    if value is not None:
        require(float(value) >= 0, f"{label} must be non-negative")


def parse_rfc3339(value: Any, label: str) -> datetime:
    require(isinstance(value, str) and RFC3339.fullmatch(value) is not None, f"{label} must be RFC3339")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        fail(f"{label} is not parseable RFC3339: {exc}")
    require(parsed.tzinfo is not None and parsed.utcoffset() is not None, f"{label} must include a timezone")
    return parsed


def require_keys(value: dict[str, Any], keys: tuple[str, ...], label: str) -> None:
    missing = [key for key in keys if key not in value]
    require(not missing, f"{label} missing required field(s): {', '.join(missing)}")


def validate_envelope(value: Any, label: str) -> dict[str, Any]:
    envelope = require_dict(value, label)
    require_keys(envelope, ("code", "message"), label)
    require_int(envelope["code"], f"{label}.code")
    require_string(envelope["message"], f"{label}.message", non_empty=False)
    if "reason" in envelope:
        require_string(envelope["reason"], f"{label}.reason", non_empty=False)
    if "metadata" in envelope:
        metadata = require_dict(envelope["metadata"], f"{label}.metadata")
        for key, item in metadata.items():
            require_string(key, f"{label}.metadata key", non_empty=False)
            require_string(item, f"{label}.metadata[{key!r}]", non_empty=False)
    if envelope["code"] == 0:
        require("data" in envelope, f"{label} success envelope must contain data")
    return envelope


def validate_last_test(value: Any, label: str) -> None:
    item = require_dict(value, label)
    require_keys(
        item,
        ("at", "account_id", "account_name", "model", "success", "first_response_ms", "total_ms"),
        label,
    )
    parse_rfc3339(item["at"], f"{label}.at")
    require_id(item["account_id"], f"{label}.account_id")
    require_string(item["account_name"], f"{label}.account_name")
    require_string(item["model"], f"{label}.model")
    require(isinstance(item["success"], bool), f"{label}.success must be boolean")
    require_duration(item["first_response_ms"], f"{label}.first_response_ms")
    require_duration(item["total_ms"], f"{label}.total_ms")
    if item["first_response_ms"] is not None and item["total_ms"] is not None:
        require(
            item["first_response_ms"] <= item["total_ms"],
            f"{label}.first_response_ms must not exceed total_ms",
        )


def validate_catalog(value: Any, label: str = "catalog") -> None:
    catalog = require_dict(value, label)
    require_keys(catalog, ("accounts", "models", "scheduling_note"), label)
    accounts = require_list(catalog["accounts"], f"{label}.accounts")
    account_ids: set[int] = set()
    for index, account_value in enumerate(accounts):
        account = require_dict(account_value, f"{label}.accounts[{index}]")
        account_label = f"{label}.accounts[{index}]"
        require_keys(
            account,
            (
                "id",
                "name",
                "platform",
                "status",
                "priority",
                "load_factor",
                "concurrency",
                "eligible",
                "rank",
            ),
            account_label,
        )
        account_id = require_id(account["id"], f"{account_label}.id")
        require(account_id not in account_ids, f"{label} duplicate account id {account_id}")
        account_ids.add(account_id)
        for field in ("name", "platform", "status"):
            require_string(account[field], f"{account_label}.{field}")
        require_int(account["priority"], f"{account_label}.priority")
        require_number_or_null(account["load_factor"], f"{account_label}.load_factor")
        require_int(account["concurrency"], f"{account_label}.concurrency", 0)
        require(isinstance(account["eligible"], bool), f"{account_label}.eligible must be boolean")
        require_int(account["rank"], f"{account_label}.rank", 1)
        if "reason" in account:
            require_string(account["reason"], f"{account_label}.reason", non_empty=False)

    models = require_list(catalog["models"], f"{label}.models")
    model_ids: set[str] = set()
    for index, model_value in enumerate(models):
        model = require_dict(model_value, f"{label}.models[{index}]")
        model_label = f"{label}.models[{index}]"
        require_keys(
            model,
            (
                "id",
                "upstream_support",
                "downstream_allowed",
                "source",
                "account_ids",
                "mapping_effective",
            ),
            model_label,
        )
        model_id = require_string(model["id"], f"{model_label}.id")
        require(model_id not in model_ids, f"{label} duplicate model id {model_id!r}")
        model_ids.add(model_id)
        if "upstream_model" in model:
            require_string(model["upstream_model"], f"{model_label}.upstream_model")
        require(model["upstream_support"] in UPSTREAM_SUPPORT, f"{model_label}.upstream_support is invalid")
        allowed = model["downstream_allowed"]
        require(allowed is None or isinstance(allowed, bool), f"{model_label}.downstream_allowed must be boolean/null")
        require_string(model["source"], f"{model_label}.source")
        listed_accounts = require_list(model["account_ids"], f"{model_label}.account_ids")
        for account_index, account_id in enumerate(listed_accounts):
            require_id(account_id, f"{model_label}.account_ids[{account_index}]")
        require(isinstance(model["mapping_effective"], bool), f"{model_label}.mapping_effective must be boolean")
        if "last_test" in model and model["last_test"] is not None:
            validate_last_test(model["last_test"], f"{model_label}.last_test")
    require_string(catalog["scheduling_note"], f"{label}.scheduling_note", non_empty=False)


def validate_attempt(value: Any, label: str) -> dict[str, Any]:
    attempt = require_dict(value, label)
    require_keys(
        attempt,
        (
            "id",
            "index",
            "account_id",
            "account_name",
            "requested_model",
            "model",
            "endpoint",
            "status",
            "started_at",
            "completed_at",
            "first_response_ms",
            "total_ms",
        ),
        label,
    )
    require_id(attempt["id"], f"{label}.id")
    require_int(attempt["index"], f"{label}.index", 0)
    require_id(attempt["account_id"], f"{label}.account_id")
    for field in ("account_name", "requested_model", "model", "endpoint"):
        require_string(attempt[field], f"{label}.{field}")
    require(attempt["status"] in ATTEMPT_STATUSES, f"{label}.status is invalid")
    started_at = parse_rfc3339(attempt["started_at"], f"{label}.started_at")
    completed_at = attempt["completed_at"]
    if attempt["status"] == "running":
        require(completed_at is None, f"{label}.running attempt must have completed_at=null")
    else:
        require(completed_at is not None, f"{label}.{attempt['status']} attempt needs completed_at")
    if completed_at is not None:
        completed_time = parse_rfc3339(completed_at, f"{label}.completed_at")
        require(completed_time >= started_at, f"{label}.completed_at precedes started_at")
    require_duration(attempt["first_response_ms"], f"{label}.first_response_ms")
    require_duration(attempt["total_ms"], f"{label}.total_ms")
    if attempt["first_response_ms"] is not None and attempt["total_ms"] is not None:
        require(
            attempt["first_response_ms"] <= attempt["total_ms"],
            f"{label}.first_response_ms must not exceed total_ms",
        )
    if "status_code" in attempt:
        status_code = require_int(attempt["status_code"], f"{label}.status_code")
        require(100 <= status_code <= 599, f"{label}.status_code must be HTTP-like")
    for field in ("error", "reason"):
        if field in attempt:
            require_string(attempt[field], f"{label}.{field}", non_empty=False)
    return attempt


def validate_conversation(value: Any, label: str = "conversation") -> dict[str, Any]:
    conversation = require_dict(value, label)
    require_keys(
        conversation,
        ("id", "group_id", "account_id", "model", "title", "created_at", "updated_at"),
        label,
    )
    require_id(conversation["id"], f"{label}.id")
    require_id(conversation["group_id"], f"{label}.group_id")
    if conversation["account_id"] is not None:
        require_id(conversation["account_id"], f"{label}.account_id")
    require_string(conversation["model"], f"{label}.model")
    require_string(conversation["title"], f"{label}.title", non_empty=False)
    created_at = parse_rfc3339(conversation["created_at"], f"{label}.created_at")
    updated_at = parse_rfc3339(conversation["updated_at"], f"{label}.updated_at")
    require(updated_at >= created_at, f"{label}.updated_at precedes created_at")
    return conversation


def validate_turn(value: Any, label: str = "turn", require_terminal: bool = False) -> dict[str, Any]:
    turn = require_dict(value, label)
    require_keys(
        turn,
        (
            "id",
            "conversation_id",
            "prompt",
            "content",
            "status",
            "created_at",
            "completed_at",
            "first_response_ms",
            "total_ms",
            "attempts",
        ),
        label,
    )
    require_id(turn["id"], f"{label}.id")
    require_id(turn["conversation_id"], f"{label}.conversation_id")
    require_string(turn["prompt"], f"{label}.prompt", non_empty=False)
    require_string(turn["content"], f"{label}.content", non_empty=False)
    require(turn["status"] in TURN_STATUSES, f"{label}.status is invalid")
    created_at = parse_rfc3339(turn["created_at"], f"{label}.created_at")
    completed_at = turn["completed_at"]
    if turn["status"] == "running":
        require(completed_at is None, f"{label}.running turn must have completed_at=null")
    else:
        require(completed_at is not None, f"{label}.{turn['status']} turn needs completed_at")
    if completed_at is not None:
        completed_time = parse_rfc3339(completed_at, f"{label}.completed_at")
        require(completed_time >= created_at, f"{label}.completed_at precedes created_at")
    require_duration(turn["first_response_ms"], f"{label}.first_response_ms")
    require_duration(turn["total_ms"], f"{label}.total_ms")
    if turn["first_response_ms"] is not None and turn["total_ms"] is not None:
        require(
            turn["first_response_ms"] <= turn["total_ms"],
            f"{label}.first_response_ms must not exceed total_ms",
        )
    if "error" in turn:
        require_string(turn["error"], f"{label}.error", non_empty=False)

    attempts = require_list(turn["attempts"], f"{label}.attempts")
    attempt_ids: set[int] = set()
    for index, attempt_value in enumerate(attempts):
        attempt = validate_attempt(attempt_value, f"{label}.attempts[{index}]")
        attempt_id = attempt["id"]
        require(attempt_id not in attempt_ids, f"{label} duplicate attempt id {attempt_id}")
        attempt_ids.add(attempt_id)
    if require_terminal:
        require(turn["status"] != "running", f"{label} terminal payload cannot be running")
        require(all(item["status"] != "running" for item in attempts), f"{label} terminal turn has running attempt")
        if turn["status"] == "succeeded":
            require(any(item["status"] == "succeeded" for item in attempts), f"{label} succeeded turn has no succeeded attempt")
        elif turn["status"] == "failed":
            require(any(item["status"] == "failed" for item in attempts), f"{label} failed turn has no failed attempt")
        elif turn["status"] == "cancelled":
            require(any(item["status"] == "cancelled" for item in attempts), f"{label} cancelled turn has no cancelled attempt")
    return turn


def validate_conversation_list(value: Any, label: str = "conversation_list") -> None:
    page = require_dict(value, label)
    require_keys(page, ("items", "total"), label)
    items = require_list(page["items"], f"{label}.items")
    for index, item in enumerate(items):
        validate_conversation(item, f"{label}.items[{index}]")
    total = require_int(page["total"], f"{label}.total", 0)
    require(total >= len(items), f"{label}.total cannot be less than returned items")
    for field in ("page", "page_size", "pages"):
        if field in page:
            require_int(page[field], f"{label}.{field}", 1)


def validate_conversation_detail(value: Any, label: str = "conversation_detail") -> None:
    detail = require_dict(value, label)
    require_keys(detail, ("conversation", "turns"), label)
    conversation = validate_conversation(detail["conversation"], f"{label}.conversation")
    turns = require_list(detail["turns"], f"{label}.turns")
    seen_ids: set[int] = set()
    for index, turn_value in enumerate(turns):
        turn = validate_turn(turn_value, f"{label}.turns[{index}]")
        require(turn["conversation_id"] == conversation["id"], f"{label}.turns[{index}] belongs to another conversation")
        require(turn["id"] not in seen_ids, f"{label} duplicate turn id {turn['id']}")
        seen_ids.add(turn["id"])


def normalise_sse_wire(value: Any, label: str) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        require(all(isinstance(item, str) for item in value), f"{label} array must contain wire strings")
        return "\n\n".join(value)
    fail(f"{label} must be an SSE wire string or array of wire strings")


def parse_sse_wire(value: Any, label: str) -> list[dict[str, Any]]:
    wire = normalise_sse_wire(value, label).replace("\r\n", "\n").replace("\r", "\n")
    blocks = [block for block in re.split(r"\n\n", wire.strip()) if block.strip()]
    require(bool(blocks), f"{label} must contain at least one SSE event")
    events: list[dict[str, Any]] = []
    for block_index, block in enumerate(blocks):
        data_lines: list[str] = []
        event_name: str | None = None
        for line in block.split("\n"):
            if not line or line.startswith(":"):
                continue
            if line.startswith("event:"):
                event_name = line[len("event:") :].strip()
            elif line.startswith("data:"):
                data = line[len("data:") :]
                if data.startswith(" "):
                    data = data[1:]
                data_lines.append(data)
            elif line.startswith("id:") or line.startswith("retry:"):
                continue
            else:
                fail(f"{label} event {block_index} has unsupported SSE line: {line!r}")
        require(bool(data_lines), f"{label} event {block_index} has no data line")
        raw_json = "\n".join(data_lines)
        require(raw_json != "[DONE]", f"{label} event {block_index} cannot use [DONE]")
        try:
            event = json.loads(raw_json)
        except json.JSONDecodeError as exc:
            fail(f"{label} event {block_index} data is not JSON: {exc.msg}")
        event = require_dict(event, f"{label} event {block_index} data")
        if event_name is not None:
            require(event_name == event.get("type"), f"{label} event {block_index} event/type mismatch")
        events.append(event)
    return events


def validate_sse_stream(value: Any, label: str) -> None:
    events = parse_sse_wire(value, label)
    first_conversation_id: int | None = None
    first_turn_id: int | None = None
    previous_at: datetime | None = None
    started_attempts: dict[int, dict[str, Any]] = {}
    failed_attempt_ids: set[int] = set()
    terminal_count = 0

    for index, event in enumerate(events):
        event_label = f"{label}.event[{index}]"
        require_keys(event, ("type", "conversation_id", "turn_id", "seq", "at"), event_label)
        event_type = require_string(event["type"], f"{event_label}.type")
        require(event_type in EVENT_TYPES, f"{event_label}.type is not in the fixed event enum")
        conversation_id = require_id(event["conversation_id"], f"{event_label}.conversation_id")
        turn_id = require_id(event["turn_id"], f"{event_label}.turn_id")
        sequence = require_int(event["seq"], f"{event_label}.seq", 1)
        require(sequence == index + 1, f"{event_label}.seq must be contiguous and start at 1")
        at = parse_rfc3339(event["at"], f"{event_label}.at")
        if previous_at is not None:
            require(at >= previous_at, f"{event_label}.at goes backwards")
        previous_at = at
        if first_conversation_id is None:
            first_conversation_id = conversation_id
            first_turn_id = turn_id
        require(conversation_id == first_conversation_id, f"{event_label} changes conversation_id")
        require(turn_id == first_turn_id, f"{event_label} changes turn_id")

        if "error" in event:
            require_string(event["error"], f"{event_label}.error", non_empty=False)
        if "delta" in event:
            require_string(event["delta"], f"{event_label}.delta", non_empty=False)
        if "attempt" in event and event["attempt"] is not None:
            validate_attempt(event["attempt"], f"{event_label}.attempt")

        if event_type == "attempt_started":
            require("attempt" in event and event["attempt"] is not None, f"{event_label} needs attempt")
            attempt = event["attempt"]
            require(attempt["status"] == "running", f"{event_label}.attempt.status must be running")
            attempt_id = attempt["id"]
            require(attempt_id not in started_attempts, f"{event_label} starts duplicate attempt {attempt_id}")
            started_attempts[attempt_id] = attempt
        elif event_type == "attempt_failed":
            require("attempt" in event and event["attempt"] is not None, f"{event_label} needs attempt")
            attempt = event["attempt"]
            require(attempt["status"] == "failed", f"{event_label}.attempt.status must be failed")
            attempt_id = attempt["id"]
            require(attempt_id in started_attempts, f"{event_label} must follow attempt_started for {attempt_id}")
            require(attempt_id not in failed_attempt_ids, f"{event_label} duplicates attempt_failed for {attempt_id}")
            failed_attempt_ids.add(attempt_id)
        elif event_type == "content_delta":
            require("delta" in event, f"{event_label} content_delta needs delta")

        if event_type in TERMINAL_EVENT_STATUS:
            terminal_count += 1
            require(index == len(events) - 1, f"{event_label} terminal event must be last")
            require("turn" in event and event["turn"] is not None, f"{event_label} must carry complete persisted turn")
            turn = validate_turn(event["turn"], f"{event_label}.turn", require_terminal=True)
            require(turn["id"] == turn_id, f"{event_label}.turn.id does not match event turn_id")
            require(turn["conversation_id"] == conversation_id, f"{event_label}.turn belongs to another conversation")
            require(turn["status"] == TERMINAL_EVENT_STATUS[event_type], f"{event_label} status does not match turn status")
            if event_type == "turn_failed":
                turn_error = str(turn.get("error", "")).strip()
                event_error = str(event.get("error", "")).strip()
                require(bool(turn_error or event_error), f"{event_label} failed terminal needs error")
            final_attempt_ids = {attempt["id"] for attempt in turn["attempts"]}
            require(
                set(started_attempts).issubset(final_attempt_ids),
                f"{event_label}.turn.attempts omits a streamed attempt",
            )
            for failed_id in failed_attempt_ids:
                require(failed_id in final_attempt_ids, f"{event_label}.turn.attempts omits failed attempt {failed_id}")

    require(terminal_count == 1, f"{label} must contain exactly one terminal event")


def _event_wire(event: dict[str, Any], event_type: str) -> str:
    return f"event: {event_type}\ndata: {json.dumps(event, separators=(',', ':'), ensure_ascii=False)}\n\n"


def _attempt(
    attempt_id: int,
    index: int,
    status: str,
    started_at: str,
    completed_at: str | None,
    first_response_ms: int | None,
    total_ms: int | None,
    error: str | None = None,
) -> dict[str, Any]:
    value: dict[str, Any] = {
        "id": attempt_id,
        "index": index,
        "account_id": 101 if attempt_id in (501, 502) else 102,
        "account_name": "qa-account-a" if attempt_id in (501, 502) else "qa-account-b",
        "requested_model": "gpt-5.6-luna",
        "model": "gpt-5.6-luna",
        "endpoint": "https://example.invalid/v1/responses",
        "status": status,
        "started_at": started_at,
        "completed_at": completed_at,
        "first_response_ms": first_response_ms,
        "total_ms": total_ms,
    }
    if error is not None:
        value["error"] = error
        value["reason"] = "upstream_unavailable"
        value["status_code"] = 503
    return value


_SUCCESS_TURN = {
    "id": 401,
    "conversation_id": 301,
    "prompt": "Say hello",
    "content": "Hello from the offline contract fixture.",
    "status": "succeeded",
    "created_at": "2026-09-11T08:00:00Z",
    "completed_at": "2026-09-11T08:00:01Z",
    "first_response_ms": 180,
    "total_ms": 1000,
    "attempts": [
        _attempt(501, 0, "failed", "2026-09-11T08:00:00Z", "2026-09-11T08:00:00.120Z", None, 120, "temporary upstream failure"),
        _attempt(502, 1, "succeeded", "2026-09-11T08:00:00.130Z", "2026-09-11T08:00:01Z", 180, 870),
    ],
}
_FAILED_TURN = {
    "id": 402,
    "conversation_id": 301,
    "prompt": "Fail safely",
    "content": "",
    "status": "failed",
    "created_at": "2026-09-11T08:01:00Z",
    "completed_at": "2026-09-11T08:01:00.200Z",
    "first_response_ms": None,
    "total_ms": 200,
    "attempts": [
        _attempt(503, 0, "failed", "2026-09-11T08:01:00Z", "2026-09-11T08:01:00.200Z", None, 200, "no eligible account"),
    ],
    "error": "no eligible account",
}
_CANCELLED_TURN = {
    "id": 403,
    "conversation_id": 301,
    "prompt": "Cancel safely",
    "content": "partial",
    "status": "cancelled",
    "created_at": "2026-09-11T08:02:00Z",
    "completed_at": "2026-09-11T08:02:00.300Z",
    "first_response_ms": None,
    "total_ms": 300,
    "attempts": [
        _attempt(504, 0, "cancelled", "2026-09-11T08:02:00Z", "2026-09-11T08:02:00.300Z", None, 300),
    ],
}


_SUCCESS_EVENTS = [
    {"type": "turn_started", "conversation_id": 301, "turn_id": 401, "seq": 1, "at": "2026-09-11T08:00:00Z"},
    {"type": "routing", "conversation_id": 301, "turn_id": 401, "seq": 2, "at": "2026-09-11T08:00:00.010Z"},
    {"type": "attempt_started", "conversation_id": 301, "turn_id": 401, "seq": 3, "at": "2026-09-11T08:00:00.020Z", "attempt": _attempt(501, 0, "running", "2026-09-11T08:00:00.020Z", None, None, None)},
    {"type": "attempt_failed", "conversation_id": 301, "turn_id": 401, "seq": 4, "at": "2026-09-11T08:00:00.120Z", "attempt": _attempt(501, 0, "failed", "2026-09-11T08:00:00.020Z", "2026-09-11T08:00:00.120Z", None, 100, "temporary upstream failure")},
    {"type": "attempt_started", "conversation_id": 301, "turn_id": 401, "seq": 5, "at": "2026-09-11T08:00:00.130Z", "attempt": _attempt(502, 1, "running", "2026-09-11T08:00:00.130Z", None, None, None)},
    {"type": "content_delta", "conversation_id": 301, "turn_id": 401, "seq": 6, "at": "2026-09-11T08:00:00.310Z", "delta": "Hello from the offline contract fixture."},
    {"type": "turn_completed", "conversation_id": 301, "turn_id": 401, "seq": 7, "at": "2026-09-11T08:00:01Z", "turn": _SUCCESS_TURN},
]
_FAILED_EVENTS = [
    {"type": "turn_started", "conversation_id": 301, "turn_id": 402, "seq": 1, "at": "2026-09-11T08:01:00Z"},
    {"type": "routing", "conversation_id": 301, "turn_id": 402, "seq": 2, "at": "2026-09-11T08:01:00.010Z"},
    {"type": "attempt_started", "conversation_id": 301, "turn_id": 402, "seq": 3, "at": "2026-09-11T08:01:00.020Z", "attempt": _attempt(503, 0, "running", "2026-09-11T08:01:00.020Z", None, None, None)},
    {"type": "attempt_failed", "conversation_id": 301, "turn_id": 402, "seq": 4, "at": "2026-09-11T08:01:00.200Z", "attempt": _attempt(503, 0, "failed", "2026-09-11T08:01:00.020Z", "2026-09-11T08:01:00.200Z", None, 180, "no eligible account")},
    {"type": "turn_failed", "conversation_id": 301, "turn_id": 402, "seq": 5, "at": "2026-09-11T08:01:00.200Z", "error": "no eligible account", "turn": _FAILED_TURN},
]
_CANCELLED_EVENTS = [
    {"type": "turn_started", "conversation_id": 301, "turn_id": 403, "seq": 1, "at": "2026-09-11T08:02:00Z"},
    {"type": "routing", "conversation_id": 301, "turn_id": 403, "seq": 2, "at": "2026-09-11T08:02:00.010Z"},
    {"type": "attempt_started", "conversation_id": 301, "turn_id": 403, "seq": 3, "at": "2026-09-11T08:02:00.020Z", "attempt": _attempt(504, 0, "running", "2026-09-11T08:02:00.020Z", None, None, None)},
    {"type": "content_delta", "conversation_id": 301, "turn_id": 403, "seq": 4, "at": "2026-09-11T08:02:00.100Z", "delta": "partial"},
    {"type": "turn_cancelled", "conversation_id": 301, "turn_id": 403, "seq": 5, "at": "2026-09-11T08:02:00.300Z", "turn": _CANCELLED_TURN},
]

_CONVERSATION = {
    "id": 301,
    "group_id": 7,
    "account_id": None,
    "model": "gpt-5.6-luna",
    "title": "Offline QA conversation",
    "created_at": "2026-09-11T07:59:00Z",
    "updated_at": "2026-09-11T08:02:00.300Z",
}

BUILTIN_FIXTURE: dict[str, Any] = {
    "responses": [
        {
            "name": "catalog",
            "envelope": {
                "code": 0,
                "message": "success",
                "data": {
                    "accounts": [
                        {
                            "id": 101,
                            "name": "qa-account-a",
                            "platform": "openai",
                            "status": "active",
                            "priority": 10,
                            "load_factor": 0.35,
                            "concurrency": 2,
                            "eligible": True,
                            "reason": "",
                            "rank": 1,
                        },
                        {
                            "id": 102,
                            "name": "qa-account-b",
                            "platform": "openai",
                            "status": "active",
                            "priority": 20,
                            "load_factor": None,
                            "concurrency": 1,
                            "eligible": True,
                            "rank": 2,
                        },
                    ],
                    "models": [
                        {
                            "id": "gpt-5.6-luna",
                            "upstream_model": "gpt-5.6-luna",
                            "upstream_support": "declared",
                            "downstream_allowed": True,
                            "source": "account_mapping",
                            "account_ids": [101, 102],
                            "mapping_effective": False,
                            "last_test": {
                                "at": "2026-09-11T08:00:01Z",
                                "account_id": 102,
                                "account_name": "qa-account-b",
                                "model": "gpt-5.6-luna",
                                "success": True,
                                "first_response_ms": 180,
                                "total_ms": 1000,
                            },
                        },
                        {
                            "id": "unknown-model",
                            "upstream_support": "unknown",
                            "downstream_allowed": None,
                            "source": "not_observed",
                            "account_ids": [],
                            "mapping_effective": False,
                        },
                    ],
                    "scheduling_note": "rank reflects the current scheduler candidate order; it is not a probability.",
                },
            },
        },
        {"name": "conversation_create", "envelope": {"code": 0, "message": "success", "data": _CONVERSATION}},
        {
            "name": "conversation_list",
            "envelope": {"code": 0, "message": "success", "data": {"items": [_CONVERSATION], "total": 1, "page": 1, "page_size": 20, "pages": 1}},
        },
        {
            "name": "conversation_detail",
            "envelope": {
                "code": 0,
                "message": "success",
                "data": {
                    "conversation": _CONVERSATION,
                    "turns": [_SUCCESS_TURN, _FAILED_TURN, _CANCELLED_TURN],
                },
            },
        },
    ],
    "sse_streams": {
        "success": "".join(_event_wire(event, event["type"]) for event in _SUCCESS_EVENTS),
        "failed": "".join(_event_wire(event, event["type"]) for event in _FAILED_EVENTS),
        "cancelled": "".join(_event_wire(event, event["type"]) for event in _CANCELLED_EVENTS),
    },
}


def validate_fixture(fixture: Any) -> None:
    root = require_dict(fixture, "fixture")
    responses = require_list(root.get("responses"), "fixture.responses")
    payloads: dict[str, Any] = {}
    for index, response_value in enumerate(responses):
        response = require_dict(response_value, f"fixture.responses[{index}]")
        require_keys(response, ("name", "envelope"), f"fixture.responses[{index}]")
        name = require_string(response["name"], f"fixture.responses[{index}].name")
        require(name not in payloads, f"fixture duplicate response name {name!r}")
        envelope = validate_envelope(response["envelope"], f"fixture.responses[{index}].envelope")
        if envelope["code"] == 0:
            payloads[name] = envelope["data"]

    for name in ("catalog", "conversation_create", "conversation_list", "conversation_detail"):
        require(name in payloads, f"fixture is missing successful response {name!r}")
    validate_catalog(payloads["catalog"])
    validate_conversation(payloads["conversation_create"], "conversation_create.data")
    validate_conversation_list(payloads["conversation_list"], "conversation_list.data")
    validate_conversation_detail(payloads["conversation_detail"], "conversation_detail.data")

    streams = root.get("sse_streams")
    if streams is None and "sse" in root:
        streams = {"default": root["sse"]}
    streams = require_dict(streams, "fixture.sse_streams")
    require(bool(streams), "fixture.sse_streams must not be empty")
    for name, stream in streams.items():
        require_string(name, "fixture.sse_streams key")
        validate_sse_stream(stream, f"fixture.sse_streams[{name!r}]")


def read_text(repo_root: Path, relative_path: str) -> str:
    path = repo_root / relative_path
    if not path.is_file():
        return ""
    return path.read_text(encoding="utf-8", errors="replace")


def first_line_with(text: str, snippets: tuple[str, ...]) -> int | None:
    for line_number, line in enumerate(text.splitlines(), 1):
        if any(snippet in line for snippet in snippets):
            return line_number
    return None


BASELINE_EVIDENCE: tuple[tuple[str, str, tuple[str, ...]], ...] = (
    (
        "JSON response envelope",
        "backend/internal/pkg/response/response.go",
        ("type Response struct", '`json:"code"`', '`json:"message"`', '`json:"reason,omitempty"`', '`json:"metadata,omitempty"`', '`json:"data,omitempty"`'),
    ),
    (
        "admin auth and audit middleware",
        "backend/internal/server/routes/admin.go",
        ('admin := v1.Group("/admin")', "admin.Use(gin.HandlerFunc(adminAuth))", "admin.Use(gin.HandlerFunc(auditLog))"),
    ),
    (
        "legacy debug routes",
        "backend/internal/server/routes/admin.go",
        ('groups.POST("/:id/debug-test"', 'accounts.POST("/:id/debug-test"'),
    ),
    (
        "legacy debug handler and upstream call",
        "backend/internal/handler/admin/account_handler.go",
        ("func (h *AccountHandler) DebugTest(", "func (h *AccountHandler) DebugTestGroup("),
    ),
    (
        "legacy debug capture calls the account connection test",
        "backend/internal/service/account_test_debug.go",
        ("func (s *AccountTestService) TestAccountDebug(", "s.TestAccountConnection("),
    ),
    (
        "passive channel monitor migration",
        "backend/migrations/194_channel_monitor_v2.sql",
        ("Passive channel monitor V2", "channel_monitor_v2_metrics_1m", "never from active probes"),
    ),
    (
        "passive channel monitor aggregation and retention",
        "backend/internal/repository/channel_monitor_v2_aggregation.go",
        ("channelMonitorV2RetentionMax", "func (r *channelMonitorV2Repository) RecomputeRange", "pruneChannelMonitorV2Retention"),
    ),
    (
        "many-to-many account-group schema",
        "backend/ent/schema/account.go",
        ('Through("account_groups", AccountGroup.Type)',),
    ),
    (
        "account-group composite relationship",
        "backend/ent/schema/account_group.go",
        ('field.ID("account_id", "group_id")', "field.Int64(\"account_id\")", "field.Int64(\"group_id\")"),
    ),
    (
        "group account repository query",
        "backend/internal/repository/account_repo.go",
        ("func (r *accountRepository) queryAccountsByGroup", "Where(dbaccountgroup.GroupIDEQ(groupID))", "HasAccountWith"),
    ),
    (
        "simple mode platform-wide pool",
        "backend/internal/service/openai_gateway_scheduling.go",
        ("RunModeSimple", "ListSchedulableByPlatform(ctx, platform)"),
    ),
    (
        "simple mode sticky membership weakness",
        "backend/internal/service/openai_gateway_scheduling.go",
        ("func (s *OpenAIGatewayService) openAIAccountMatchesSchedulingGroup", "return account != nil"),
    ),
    (
        "simple mode regression evidence",
        "backend/internal/service/openai_guardian_affinity_test.go",
        ("TestOpenAIGatewayService_PreviousResponseSimpleModeIgnoresGroupMembership",),
    ),
    (
        "scheduler layers",
        "backend/internal/service/openai_account_scheduler.go",
        ("selectAccountByPreviousResponseIDForCapability", "GuardianParentAccountID", "selectBySessionHash", "selectByLoadBalance"),
    ),
    (
        "scheduler Top-K and weighted order",
        "backend/internal/service/openai_account_scheduler.go",
        ("selectTopKOpenAICandidates", "buildOpenAIWeightedSelectionOrder", "isOpenAIAccountCandidateBetter"),
    ),
    (
        "scheduler score factors",
        "backend/internal/service/openai_account_scheduler.go",
        ("weights.Priority*priorityFactor", "weights.ErrorRate*errorFactor", "weights.TTFT*ttftFactor", "weights.Reset*resetFactor", "weights.QuotaHeadroom*quotaHeadroomFactor", "weights.UpstreamCost"),
    ),
    (
        "scheduler concurrency and DB recheck",
        "backend/internal/service/openai_account_scheduler.go",
        ("tryAcquireOpenAIAccountSlot", "recheckSelectedOpenAIAccountFromDB", "AccountWaitPlan"),
    ),
    (
        "gateway concurrency acquire",
        "backend/internal/service/openai_gateway_scheduling.go",
        ("AcquireAccountSlot", "AccountWaitPlan", "ReleaseFunc"),
    ),
    (
        "existing account update fields",
        "backend/internal/service/account_service.go",
        ('json:"concurrency"', 'json:"priority"', 'json:"group_ids"'),
    ),
    (
        "account update validation and persistence",
        "backend/internal/service/account_service.go",
        ("validateGroupIDsExist", "s.accountRepo.Update", "s.accountRepo.BindGroups"),
    ),
    (
        "model mapping and support distinction",
        "backend/internal/service/openai_codex_model_metadata.go",
        ("GetMappedModel", "IsModelSupported", "explicitClaims"),
    ),
    (
        "OpenAI header allowlists",
        "backend/internal/service/openai_gateway_service.go",
        ("openaiAllowedHeaders", "openaiPassthroughAllowedHeaders"),
    ),
    (
        "CC raw header allowlist",
        "backend/internal/service/openai_gateway_chat_completions_raw.go",
        ("openaiCCRawAllowedHeaders", "openaiAllowedHeaders"),
    ),
    (
        "Anthropic/common header allowlist",
        "backend/internal/service/gateway_service.go",
        ("var allowedHeaders = map[string]bool",),
    ),
    (
        "header allowlist actual use",
        "backend/internal/service/openai_gateway_forward.go",
        ("Whitelist passthrough headers", "openaiAllowedHeaders[lowerKey]"),
    ),
    (
        "WS response/account map",
        "backend/internal/service/openai_ws_state_store.go",
        ("response_id -> account_id", "BindResponseAccount", "openAIWSResponseAccountMapKey", "fmt.Sprintf(\"%d:%s\""),
    ),
    (
        "HTTP owner binding",
        "backend/internal/service/openai_gateway_response_handling.go",
        ("ValidateOpenAIHTTPResponseOwner", "userID", "apiKeyID"),
    ),
)


def run_baseline_evidence(repo_root: Path) -> list[str]:
    failures: list[str] = []
    for label, relative_path, snippets in BASELINE_EVIDENCE:
        text = read_text(repo_root, relative_path)
        missing = [snippet for snippet in snippets if snippet not in text]
        if missing:
            failures.append(f"{label}: {relative_path} missing {missing!r}")
            continue
        line = first_line_with(text, snippets)
        print(f"PASS static: {label} ({relative_path}:{line or '?'})")
    return failures


def collect_backend_sources(repo_root: Path) -> list[tuple[str, str]]:
    roots = [repo_root / "backend" / "internal", repo_root / "backend" / "migrations"]
    result: list[tuple[str, str]] = []
    for root in roots:
        if not root.is_dir():
            continue
        for path in sorted(root.rglob("*")):
            if path.is_file() and path.suffix in {".go", ".sql"}:
                result.append((str(path.relative_to(repo_root)), path.read_text(encoding="utf-8", errors="replace")))
    return result


def run_v2_implementation_evidence(repo_root: Path) -> list[str]:
    """Return missing strict-gate evidence without treating frontend labels as API."""

    sources = collect_backend_sources(repo_root)
    all_backend = "\n".join(text for _, text in sources)
    route_sources = [text for path, text in sources if path.startswith("backend/internal/server/routes/")]
    missing: list[str] = []

    if not any('"/channel-test"' in text or "'/channel-test'" in text for text in route_sources):
        missing.append("backend admin route /channel-test")
    if not any("/catalog" in text and "Catalog" in text for _, text in sources):
        missing.append("catalog endpoint/handler")
    if not any("/conversations" in text and "conversation" in text.lower() for _, text in sources):
        missing.append("conversation endpoint/handler")
    if not any("turns/stream" in text and "conversation_id" in text for _, text in sources):
        missing.append("turn stream endpoint/handler")
    if not all(event_name in all_backend for event_name in EVENT_TYPES):
        missing.append("all fixed SSE event type implementations")
    if not any(
        "client_request_id" in text and "conversation_id" in text and ("turn_id" in text or "attempt_id" in text)
        for _, text in sources
    ):
        missing.append("V2 client_request_id idempotency bound to turn/conversation")
    if not any(
        "CREATE TABLE" in text
        and "conversation" in text.lower()
        and "turn" in text.lower()
        and "attempt" in text.lower()
        for path, text in sources
        if path.startswith("backend/migrations/")
    ):
        missing.append("persistent conversation/turn/attempt migration")
    if not any(
        "owner" in text.lower()
        and "conversation" in text.lower()
        and ("user_id" in text or "api_key_id" in text)
        for _, text in sources
    ):
        missing.append("conversation owner isolation evidence")
    return missing


def load_fixture(path_value: str | None) -> tuple[dict[str, Any], str]:
    if path_value is None:
        return BUILTIN_FIXTURE, "embedded"
    path = Path(path_value).expanduser().resolve()
    require(path.is_file(), f"fixture file does not exist: {path}")
    try:
        loaded = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        fail(f"fixture is not valid JSON: {exc.msg}")
    return require_dict(loaded, "fixture"), str(path)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Offline Availability V2 contract verifier")
    parser.add_argument(
        "--repo-root",
        default=None,
        help="repository root (default: three levels above this script)",
    )
    parser.add_argument(
        "--fixture",
        default=None,
        help="path to a sanitized offline JSON fixture; no network is used",
    )
    parser.add_argument(
        "--require-v2",
        action="store_true",
        help="strict gate: require backend V2 source evidence and --fixture",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    script_repo_root = Path(__file__).resolve().parents[2]
    repo_root = Path(args.repo_root).expanduser().resolve() if args.repo_root else script_repo_root
    require(repo_root.is_dir(), f"repository root does not exist: {repo_root}")

    failures: list[str] = []
    try:
        fixture, fixture_source = load_fixture(args.fixture)
        validate_fixture(fixture)
        print(f"PASS offline contract fixture: {fixture_source}")
    except ContractError as exc:
        failures.append(f"contract: {exc}")

    failures.extend(run_baseline_evidence(repo_root))
    missing_v2 = run_v2_implementation_evidence(repo_root)

    if failures:
        print("FAIL offline verifier:")
        for failure in failures:
            print(f"  - {failure}")
        return 1

    if args.require_v2:
        strict_failures = list(missing_v2)
        if args.fixture is None:
            strict_failures.append("strict V2 gate requires --fixture from an integration run")
        if strict_failures:
            print("FAIL strict V2 gate (--require-v2):")
            for failure in strict_failures:
                print(f"  - {failure}")
            return 1
        print("PASS strict V2 gate (--require-v2): source evidence and sanitized fixture present")
        return 0

    if missing_v2:
        print("V2 NOT IMPLEMENTED at the selected baseline (informational default mode):")
        for item in missing_v2:
            print(f"  - {item}")
    else:
        print("V2 implementation source evidence present; use --require-v2 with --fixture for the strict gate.")
    print("INTEGRATION NOT RUN: this verifier is offline and makes no live success claim.")
    print("PASS default offline verifier")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ContractError as exc:
        print(f"FAIL offline verifier: {exc}", file=sys.stderr)
        raise SystemExit(1)
