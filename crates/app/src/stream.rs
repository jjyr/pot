//! Bounded SSE decoder. Decode UTF-8 only after assembling a complete line.
use std::io::{self, BufRead, Read};
const LIMIT: usize = 8 * 1024 * 1024;
#[derive(Debug, PartialEq, Eq)]
pub struct Frame {
    pub event: String,
    pub data: String,
}
pub fn next(reader: &mut impl BufRead) -> io::Result<Option<Frame>> {
    let mut event = String::from("message");
    let mut data = String::new();
    let mut size = 0;
    loop {
        let mut line = Vec::new();
        let n = reader
            .take((LIMIT - size + 1) as u64)
            .read_until(b'\n', &mut line)?;
        if n == 0 {
            if data.is_empty() {
                return Ok(None);
            }
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "Incomplete SSE frame",
            ));
        }
        size += n;
        if size > LIMIT {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "SSE frame too large",
            ));
        }
        let line = std::str::from_utf8(&line)
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "Invalid SSE UTF-8"))?
            .trim_end_matches(['\r', '\n']);
        if line.is_empty() {
            if !data.is_empty() {
                data.pop();
                return Ok(Some(Frame { event, data }));
            }
            size = 0;
            event = "message".into();
            continue;
        }
        if line.starts_with(':') {
            continue;
        }
        let (field, value) = line.split_once(':').unwrap_or((line, ""));
        let value = value.strip_prefix(' ').unwrap_or(value);
        match field {
            "event" => event = value.into(),
            "data" => {
                data.push_str(value);
                data.push('\n');
            }
            _ => {}
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn handles_comments_crlf_multiline_and_fragmented_unicode() {
        // Capacity one forces reads to split every UTF-8 codepoint.
        let mut r = io::BufReader::with_capacity(
            1,
            io::Cursor::new(": ping\r\n\r\nevent: snapshot\r\ndata: 中文\r\ndata: next\r\n\r\n"),
        );
        assert_eq!(
            next(&mut r).unwrap(),
            Some(Frame {
                event: "snapshot".into(),
                data: "中文\nnext".into()
            })
        );
        assert_eq!(next(&mut r).unwrap(), None);
    }
    #[test]
    fn truncated_event_is_not_applied() {
        assert_eq!(
            next(&mut io::Cursor::new("data: partial\n"))
                .unwrap_err()
                .kind(),
            io::ErrorKind::UnexpectedEof
        );
    }
}
