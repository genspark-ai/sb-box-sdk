import pytest

from sbbox import errors


@pytest.mark.parametrize(
    "status,body,cls,code",
    [
        (400, {"detail": "bad"}, errors.ValidationError, None),
        (401, {"detail": "nope"}, errors.AuthenticationError, None),
        (
            402,
            {"detail": {"code": "sb_box_insufficient_credits", "balance": 0, "message": "no credits"}},
            errors.InsufficientCreditsError,
            "sb_box_insufficient_credits",
        ),
        (
            403,
            {"detail": {"code": "sb_box_plan_required", "plan": "plus", "upgrade": "https://x"}},
            errors.PlanRequiredError,
            "sb_box_plan_required",
        ),
        (403, {"detail": "forbidden"}, errors.ForbiddenError, None),
        (404, {"detail": "sandbox not found"}, errors.NotFoundError, None),
        (
            404,
            {"detail": {"code": "sb_box_region_retired", "region": "old", "message": "gone"}},
            errors.RegionRetiredError,
            "sb_box_region_retired",
        ),
        (408, {"error": "timed out", "stage": "timeout", "killed": True}, errors.CommandTimeoutError, None),
        (
            409,
            {"detail": {"code": "sb_box_concurrency_limit", "limit": 20, "current": 20}},
            errors.LimitError,
            "sb_box_concurrency_limit",
        ),
        (
            409,
            {"detail": {"code": "sb_box_spec_limit", "limit": 4, "requested": 8}},
            errors.LimitError,
            "sb_box_spec_limit",
        ),
        (
            409,
            {"detail": {"code": "sb_box_build_concurrency_limit", "limit": 1}},
            errors.LimitError,
            "sb_box_build_concurrency_limit",
        ),
        (
            409,
            {"detail": {"code": "sb_box_interpreter_unavailable"}},
            errors.InterpreterUnavailableError,
            "sb_box_interpreter_unavailable",
        ),
        (409, {"detail": "already building"}, errors.ConflictError, None),
        (
            410,
            {"error": "disk lost", "reason": "disk_lost", "recovery": ["rollback"]},
            errors.DiskLostError,
            "disk_lost",
        ),
        (
            422,
            {"status": -2, "message": "invalid", "errors": [{"loc": ["body", "timeout"], "msg": "int"}]},
            errors.ValidationError,
            None,
        ),
        (
            428,
            {"reason": "memory_expired", "recovery": ["reset"]},
            errors.MemoryExpiredError,
            "memory_expired",
        ),
        (499, {"detail": "client closed"}, errors.ClientDisconnectedError, None),
        (
            502,
            {"detail": {"code": "sb_box_interpreter_start_failed"}},
            errors.InterpreterStartFailedError,
            "sb_box_interpreter_start_failed",
        ),
        (502, {"detail": "bad gateway"}, errors.ServerError, None),
        (503, "Service Unavailable", errors.ServerError, None),
        (504, None, errors.ServerError, None),
    ],
)
def test_mapping(status, body, cls, code):
    e = errors.error_for_response(status, body, retry_after=2.0, request_id="rid")
    assert type(e) is cls
    assert isinstance(e, errors.SbBoxError)
    assert e.status == status and e.code == code
    assert e.retry_after == 2.0 and e.request_id == "rid"
    assert e.detail == body
    assert str(e)


def test_field_accessors():
    e = errors.error_for_response(
        409, {"detail": {"code": "sb_box_concurrency_limit", "plan": "free", "limit": 1, "current": 1}}
    )
    assert isinstance(e, errors.LimitError)
    assert (e.plan, e.limit, e.current) == ("free", 1, 1)
    e = errors.error_for_response(
        403, {"detail": {"code": "sb_box_plan_required", "plan": "plus", "upgrade": "u"}}
    )
    assert e.plan == "plus" and e.upgrade == "u"
    e = errors.error_for_response(428, {"reason": "memory_expired", "recovery": ["reset"]})
    assert e.recovery == ["reset"]
    e = errors.error_for_response(408, {"error": "t", "stage": "timeout", "killed": True})
    assert e.killed is True
    e = errors.error_for_response(422, {"status": -2, "message": "m", "errors": [{"loc": ["x"]}]})
    assert e.errors == [{"loc": ["x"]}]
    e = errors.error_for_response(402, {"detail": {"code": "sb_box_insufficient_credits", "balance": -3}})
    assert e.balance == -3


def test_redaction():
    e = errors.SbBoxError("key gsk-abc.DEF_123-x leaked")
    assert "gsk-abc" not in str(e) and "gsk-REDACTED" in e.message
    assert errors.redact("a gsk-1 b gsk-2") == "a gsk-REDACTED b gsk-REDACTED"


def test_message_extraction():
    assert errors.error_for_response(400, {"detail": {"message": "m", "code": "c"}}).message == "m"
    assert errors.error_for_response(400, {"detail": "plain"}).message == "plain"
    assert errors.error_for_response(400, {"error": "e"}).message == "e"
    assert errors.error_for_response(500, b"").message == "HTTP 500"
