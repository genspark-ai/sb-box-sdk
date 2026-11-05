import httpx
import pytest

from sbbox import errors


async def test_async_full_flow(aclient, api, monkeypatch):
    async def nosleep(s):
        return None

    monkeypatch.setattr("sbbox.sandboxes.asyncio.sleep", nosleep)
    monkeypatch.setattr("sbbox.templates.asyncio.sleep", nosleep)
    monkeypatch.setattr("sbbox._client.asyncio.sleep", nosleep, raising=False)
    async with aclient as c:
        assert (await c.regions()).default == "westus2"
        assert (await c.limits()).plan == "plus"
        assert (await c.usage(3)).days == 3
        sb = await c.sandboxes.create(metadata={"a": "b"})
        assert sb.port_url(80).startswith("https://80-")
        r = await sb.exec("false")
        assert r.exit_code == 1
        evs = [e async for e in sb.exec_stream("x")]
        assert evs[-1].exit_code == 3
        code = await sb.run_code("1+1")
        assert code.text == "2"
        assert await sb.files.write("/tmp/x", "abc") == 3
        assert await sb.files.read_text("/tmp/x") == "abc"
        assert (await sb.files.list("/tmp"))[0].name == "a.txt"
        cmd = await sb.commands.start("cat")
        await cmd.input("hi")
        await cmd.close()
        assert await cmd.wait() == 7
        assert [e.kind async for e in cmd.connect()] == ["stdout", "stderr", "gap", "done"]
        await cmd.kill()
        assert (await sb.commands.list())[0].exec_id == "bg1"
        pty = await sb.pty.open()
        await pty.input("ls\n")
        await pty.resize(1, 2)
        assert [e.kind async for e in pty.connect()] == ["data", "exit"]
        await pty.kill()
        await sb.freeze()
        assert sb.state == "frozen"
        frozen = await c.sandboxes.freeze("sbx-202")
        assert frozen.state == "frozen"
        await sb.resume(timeout=10)
        await sb.rollback()
        await sb.set_timeout(5)
        await sb.reset()
        clone = await sb.clone()
        assert clone.id == "sbx-clone"
        assert (await sb.metrics())[0].cpu_count == 1
        await sb.grant("bob")
        assert (await sb.grants())[0].grantee_id == "bob"
        await sb.revoke("bob")
        assert (await c.sandboxes.list("running"))[0].id == "sbx-abc123"
        assert (await c.sandboxes.shared())[0].id == "sbx-abc123"
        got = await c.sandboxes.get("sbx-abc123")
        await got.kill()
        b = await c.templates.build(image="x")
        assert (await c.templates.wait_ready(b.template_id, interval=0)).status == "ready"
        assert (await c.templates.list())[0].id == "tpl-base"
        assert (await c.templates.shared())[0].id == "tpl-base"
        await c.templates.grant("tpl-base", "bob")
        assert (await c.templates.grants("tpl-base"))[0].grantee_id == "bob"
        await c.templates.revoke("tpl-base", "bob")
        await c.templates.delete("tpl-base")
        with pytest.raises(errors.NotFoundError):
            await c.sandboxes.get("sbx-gone")
        with pytest.raises(errors.TemplateBuildError):
            await c.templates.wait_ready("tpl-bad")


async def test_async_retry_and_transport(aclient, api):
    api.fail_first = 1
    assert (await aclient.regions()).default == "westus2"

    async def boom(req):
        raise httpx.ConnectError("down gsk-abc")

    import sbbox

    c = sbbox.AsyncClient(
        "gsk-test-key", base_url="https://api.test", retries=0, transport=httpx.MockTransport(boom)
    )
    with pytest.raises(errors.TransportError) as ei:
        await c.regions()
    assert "gsk-abc" not in str(ei.value)
    async with sbbox.AsyncClient(
        "gsk-test-key", base_url="https://api.test", transport=httpx.MockTransport(boom)
    ) as c:
        c._core.backoff = lambda a: 0
        with pytest.raises(errors.TransportError):
            await c.regions()


async def test_async_wait_no_deadline_and_freeze_failed(aclient, api, monkeypatch):
    sb = await aclient.sandboxes.get("sbx-abc123")
    cmd = await sb.commands.start(["sleep", "5"])
    await cmd.wait()
    assert api.calls("POST", "/commands/bg1/wait")[-1].extensions["timeout"]["read"] is None
    api.fail_first = 1
    await sb.files.write("/tmp/a", b"xyz")
    assert await sb.files.read("/tmp/a") == b"xyz"

    async def _sleep(_s):
        return None

    monkeypatch.setattr("sbbox.sandboxes.asyncio.sleep", _sleep)
    api.routes[("POST", "/sandboxes/sbx-abc123/freeze")] = httpx.Response(202, json={"state": "freezing"})
    api.routes[("GET", "/sandboxes/sbx-abc123")] = httpx.Response(
        200, json={"id": "sbx-abc123", "state": "exited"}
    )
    with pytest.raises(errors.ConflictError, match="exited"):
        await sb.freeze()
