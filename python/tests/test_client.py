import os

import httpx
import pytest

import sbbox
from sbbox import errors


def test_key_resolution(monkeypatch):
    monkeypatch.delenv("SBBOX_API_KEY", raising=False)
    monkeypatch.delenv("GSK_API_KEY", raising=False)
    with pytest.raises(ValueError, match="SBBOX_API_KEY"):
        sbbox.Client()
    monkeypatch.setenv("GSK_API_KEY", "gsk-env")
    assert sbbox.Client()._core.api_key == "gsk-env"
    monkeypatch.setenv("SBBOX_API_KEY", "gsk-env2")
    assert sbbox.Client()._core.api_key == "gsk-env2"
    assert sbbox.Client("gsk-arg")._core.api_key == "gsk-arg"


def test_base_url_resolution(monkeypatch):
    monkeypatch.delenv("SBBOX_BASE_URL", raising=False)
    assert sbbox.Client("gsk-x").base_url == "https://www.genspark.ai/api/sb-box/v1"
    monkeypatch.setenv("SBBOX_BASE_URL", "https://dev.test/api/sb-box/v1/")
    assert sbbox.Client("gsk-x").base_url == "https://dev.test/api/sb-box/v1"


def test_headers_and_user_agent(client, api):
    client.regions()
    r = api.last
    assert r.headers["x-api-key"] == "gsk-test-key"
    assert r.headers["user-agent"].startswith(f"sbbox-python-sdk/{sbbox.__version__}")
    assert "api_key" not in str(r.url)


def test_account_calls(client, api):
    assert client.regions().default == "westus2"
    lim = client.limits()
    assert lim.plan == "plus" and lim.limits["maxConcurrentSandboxes"] == 20
    u = client.usage(days=7)
    assert u.days == 7 and u.credits == 12.5
    assert api.last.url.params["days"] == "7"
    with pytest.raises(ValueError):
        client.usage(days=0)


def test_401_maps_to_authentication_error(api):
    c = sbbox.Client("gsk-wrong", base_url="https://api.test", transport=httpx.MockTransport(api.handler))
    with pytest.raises(errors.AuthenticationError) as ei:
        c.regions()
    assert ei.value.status == 401


def test_retry_on_503_for_get(client, api):
    api.fail_first = 2
    assert client.regions().default == "westus2"
    assert len(api.calls("GET", "/regions")) == 3


def test_no_retry_for_create(client, api):
    api.routes[("POST", "/sandboxes")] = httpx.Response(503, json={"detail": "busy"})
    with pytest.raises(errors.ServerError):
        client.sandboxes.create()
    assert len(api.calls("POST", "/sandboxes")) == 1


def test_retries_exhausted(client, api):
    api.fail_first = 10
    with pytest.raises(errors.ServerError) as ei:
        client.regions()
    assert ei.value.status == 503
    assert len(api.calls("GET", "/regions")) == 4  # 1 + 3 retries


def test_transport_error(api):
    def boom(req):
        raise httpx.ConnectError("refused gsk-secret-leak")

    c = sbbox.Client(
        "gsk-test-key", base_url="https://api.test", retries=0, transport=httpx.MockTransport(boom)
    )
    with pytest.raises(errors.TransportError) as ei:
        c.regions()
    assert "gsk-REDACTED" in str(ei.value) and "secret-leak" not in str(ei.value)


def test_context_manager(client):
    with client as c:
        assert c is client


def test_repr_hides_key(client):
    assert "gsk-" not in repr(client)
    assert os.environ.get("never") is None
