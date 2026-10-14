//! Bounded incremental SSE framing shared by client transports.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Frame {
    pub event: String,
    pub data: String,
}
#[derive(Default)]
pub struct Decoder {
    line: Vec<u8>,
    event: String,
    data: String,
    size: usize,
}
impl Decoder {
    pub fn push(&mut self, bytes: &[u8]) -> Result<Vec<Frame>, String> {
        let mut frames = vec![];
        for byte in bytes {
            self.size += 1;
            if self.size > 8 * 1024 * 1024 {
                return Err("SSE frame exceeds limit".into());
            }
            if *byte != b'\n' {
                self.line.push(*byte);
                continue;
            }
            let line = std::str::from_utf8(&self.line)
                .map_err(|_| "Invalid SSE UTF-8")?
                .trim_end_matches('\r');
            if line.is_empty() {
                if !self.data.is_empty() {
                    self.data.pop();
                    frames.push(Frame {
                        event: if self.event.is_empty() {
                            "message".into()
                        } else {
                            std::mem::take(&mut self.event)
                        },
                        data: std::mem::take(&mut self.data),
                    });
                }
                self.event.clear();
                self.size = 0;
            } else if !line.starts_with(':') {
                let (field, value) = line.split_once(':').unwrap_or((line, ""));
                let value = value.strip_prefix(' ').unwrap_or(value);
                match field {
                    "event" => self.event = value.into(),
                    "data" => {
                        self.data.push_str(value);
                        self.data.push('\n');
                    }
                    _ => {}
                }
            }
            self.line.clear();
        }
        Ok(frames)
    }
    pub fn finish(&self) -> Result<(), String> {
        if self.line.is_empty() && self.data.is_empty() {
            Ok(())
        } else {
            Err("Incomplete SSE frame".into())
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn arbitrary_chunks_unicode_comments_and_crlf() {
        let mut decoder = Decoder::default();
        let mut frames = vec![];
        for b in ": heartbeat\r\n\r\nevent: snapshot\r\ndata: 中文\r\ndata: next\r\n\r\n".as_bytes()
        {
            frames.extend(decoder.push(&[*b]).unwrap());
        }
        assert_eq!(
            frames,
            vec![Frame {
                event: "snapshot".into(),
                data: "中文\nnext".into()
            }]
        );
        decoder.finish().unwrap();
    }
    #[test]
    fn rejects_truncation_and_oversized_frames() {
        let mut decoder = Decoder::default();
        decoder.push(b"data: partial\n").unwrap();
        assert!(decoder.finish().is_err());
        assert!(decoder.push(&vec![b'x'; 8 * 1024 * 1024]).is_err());
    }
}
