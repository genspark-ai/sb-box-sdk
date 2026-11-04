from sbbox._sse import iter_sse


def test_parses_events_across_chunk_boundaries():
    raw = 'id: 1\nevent: stdout\ndata: {"data": "a"}\n\nevent: exit\r\ndata: {"exitCode": 0}\r\n\r\n'
    chunks = [raw[i : i + 7] for i in range(0, len(raw), 7)]
    evs = list(iter_sse(chunks))
    assert [e.event for e in evs] == ["stdout", "exit"]
    assert evs[0].id == "1" and evs[0].json() == {"data": "a"}
    assert evs[1].json() == {"exitCode": 0}


def test_multiline_data_and_comments_and_flush():
    evs = list(iter_sse([': keepalive\ndata: {"a":\ndata: 1}\n\nevent: last\ndata: {}']))
    assert evs[0].event == "message" and evs[0].json() == {"a": 1}
    assert evs[1].event == "last" and evs[1].json() == {}
