import pytest

from sbbox import errors


def test_build_and_wait(client, api, monkeypatch):
    monkeypatch.setattr("sbbox.templates.time.sleep", lambda s: None)
    b = client.templates.build(
        name="py", image="python:3.12", cpu_count=2, memory_mb=4096, log_paths=["/var/log/app.log"]
    )
    assert api.body() == {
        "name": "py",
        "image": "python:3.12",
        "cpuCount": 2,
        "memoryMB": 4096,
        "diskSizeGB": 20,
        "logPaths": ["/var/log/app.log"],
    }
    assert b.template_id == "tpl-new" and b.status == "building"
    tpl = client.templates.wait_ready(b.template_id, interval=0)
    assert tpl.status == "ready" and api.build_polls == 3
    with pytest.raises(errors.TemplateBuildError) as ei:
        client.templates.wait_ready("tpl-bad")
    assert ei.value.build_error == "boom"
    with pytest.raises(errors.WaitTimeout):
        api.build_polls = -100
        client.templates.wait_ready("tpl-new", timeout=0, interval=0)


def test_build_validation(client):
    with pytest.raises(ValueError):
        client.templates.build(name="x")
    with pytest.raises(ValueError):
        client.templates.build(image="a", dockerfile="FROM a")
    with pytest.raises(ValueError):
        client.templates.build(image="a", log_paths=[str(i) for i in range(9)])
    client.templates.build(dockerfile="FROM ubuntu")


def test_list_get_delete_grants(client, api):
    assert client.templates.list()[0].namespace == "public"
    assert client.templates.shared()[0].can_create is True
    t = client.templates.get("tpl-base")
    assert t.name == "base" and t.cpu_count == 1
    client.templates.delete("tpl-base")
    assert api.last.method == "DELETE"
    g = client.templates.grant("tpl-base", "bob", expires_at="2027-01-01T00:00:00Z")
    assert api.body() == {"perm": "read", "expires_at": "2027-01-01T00:00:00Z"} and g.grantee_id == "bob"
    assert client.templates.grants("tpl-base")[0].permission == "read"
    client.templates.revoke("tpl-base", "bob")
    assert api.last.url.path.endswith("/templates/tpl-base/grants/bob")
