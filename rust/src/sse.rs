//! Minimal Server-Sent Events parser over a byte stream.

use bytes::Bytes;
use futures_util::{Stream, StreamExt};

use crate::error::{Error, Result};

/// One parsed SSE event.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SseEvent {
    pub event: String,
    pub data: String,
    pub id: Option<String>,
}

/// Incremental parser: feed chunks, drain events.
#[derive(Default)]
pub struct SseParser {
    buf: String,
    cur: SseEvent,
    has_data: bool,
}

impl SseParser {
    pub fn feed(&mut self, chunk: &[u8]) -> Vec<SseEvent> {
        self.buf.push_str(&String::from_utf8_lossy(chunk));
        let mut out = Vec::new();
        while let Some(nl) = self.buf.find('\n') {
            let mut line: String = self.buf.drain(..=nl).collect();
            if line.ends_with('\n') {
                line.pop();
            }
            if line.ends_with('\r') {
                line.pop();
            }
            if line.is_empty() {
                if self.has_data || !self.cur.event.is_empty() {
                    let mut ev = std::mem::take(&mut self.cur);
                    if ev.data.ends_with('\n') {
                        ev.data.pop();
                    }
                    if ev.event.is_empty() {
                        ev.event = "message".into();
                    }
                    out.push(ev);
                }
                self.has_data = false;
                continue;
            }
            if line.starts_with(':') {
                continue;
            }
            let (field, value) = match line.split_once(':') {
                Some((f, v)) => (f, v.strip_prefix(' ').unwrap_or(v)),
                None => (line.as_str(), ""),
            };
            match field {
                "event" => self.cur.event = value.to_owned(),
                "data" => {
                    self.cur.data.push_str(value);
                    self.cur.data.push('\n');
                    self.has_data = true;
                }
                "id" => self.cur.id = Some(value.to_owned()),
                _ => {}
            }
        }
        out
    }
}

/// Turn a byte stream into an SSE event stream.
pub fn events<S>(bytes: S) -> impl Stream<Item = Result<SseEvent>>
where
    S: Stream<Item = std::result::Result<Bytes, reqwest::Error>>,
{
    let parser = SseParser::default();
    bytes
        .scan(parser, |p, chunk| {
            let items: Vec<Result<SseEvent>> = match chunk {
                Ok(b) => p.feed(&b).into_iter().map(Ok).collect(),
                Err(e) => vec![Err(Error::transport(e))],
            };
            std::future::ready(Some(futures_util::stream::iter(items)))
        })
        .flatten()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_split_chunks() {
        let mut p = SseParser::default();
        let mut got = p.feed(b"event: stdout\nid: 1\ndata: {\"da");
        assert!(got.is_empty());
        got.extend(p.feed(b"ta\":\"hi\"}\n\nevent: exit\ndata: {\"exitCode\":0}\r\n\r\n"));
        assert_eq!(got.len(), 2);
        assert_eq!(got[0].event, "stdout");
        assert_eq!(got[0].id.as_deref(), Some("1"));
        assert_eq!(got[0].data, r#"{"data":"hi"}"#);
        assert_eq!(got[1].event, "exit");
    }
}
