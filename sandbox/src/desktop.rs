//! Optional display transport and Manager-issued, expiring viewer authority.
use anyhow::{Result, ensure};
use serde::Serialize;
use std::{
    pin::Pin,
    time::{Duration, Instant},
};
use tokio::io::{AsyncRead, AsyncWrite};

pub trait DesktopIo: AsyncRead + AsyncWrite + Send + Unpin {}
impl<T: AsyncRead + AsyncWrite + Send + Unpin> DesktopIo for T {}
pub type DesktopStream = Pin<Box<dyn DesktopIo>>;
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub enum DesktopProtocol {
    #[serde(rename = "rfb-3.8")]
    Rfb38,
}
#[derive(Clone, Copy, Debug, Default, Serialize)]
pub struct DesktopCapabilities {
    pub viewing: bool,
    pub input: bool,
    pub clipboard: bool,
    pub browser: bool,
    pub protocol: Option<DesktopProtocol>,
}
#[derive(Clone, Debug, Serialize)]
pub struct DesktopSession {
    pub id: String,
    pub sandbox_id: String,
    pub capabilities: DesktopCapabilities,
    pub expires_in_seconds: u64,
}
pub(crate) struct Viewer {
    pub session: DesktopSession,
    pub expires: Instant,
    pub generation: u64,
    pub connected: bool,
    pub control: bool,
    pub revision: u64,
    pub release_pending: bool,
    pub revoked: bool,
}
pub(crate) const LIFETIME: Duration = Duration::from_secs(3600);

/// Strict bounded RFB 3.8 / None client parser. Unknown extensions fail closed.
/// Server capabilities never imply permission to send input.
#[derive(Default)]
pub struct RfbGate {
    pending: Vec<u8>,
    handshake: usize,
    keys: std::collections::BTreeSet<u32>,
    pointer: Option<[u8; 4]>,
    qkeys: std::collections::BTreeMap<[u8; 4], Vec<u8>>,
}
impl RfbGate {
    pub fn push(&mut self, data: &[u8], control: bool, clipboard: bool) -> Result<Vec<u8>> {
        ensure!(
            self.pending.len() + data.len() <= 4 * 1024 * 1024,
            "RFB buffer limit"
        );
        self.pending.extend_from_slice(data);
        let mut out = Vec::new();
        loop {
            let p = &self.pending;
            if p.is_empty() {
                break;
            }
            let n = if self.handshake == 0 {
                12
            } else if self.handshake < 3 {
                1
            } else {
                match p[0] {
                    0 => 20,
                    3 => 10,
                    4 => 8,
                    5 => 6,
                    150 => 10,
                    255 => {
                        if p.len() < 2 {
                            break;
                        }
                        ensure!(p[1] == 0, "Unknown QEMU message");
                        12
                    }
                    248 => {
                        if p.len() < 9 {
                            break;
                        }
                        9 + p[8] as usize
                    }
                    2 => {
                        if p.len() < 4 {
                            break;
                        }
                        4 + 4 * u16::from_be_bytes([p[2], p[3]]) as usize
                    }
                    6 => {
                        if p.len() < 8 {
                            break;
                        }
                        let len = i32::from_be_bytes(p[4..8].try_into()?);
                        8 + len.unsigned_abs() as usize
                    }
                    _ => anyhow::bail!("Unsupported RFB client message"),
                }
            };
            ensure!(n <= 4 * 1024 * 1024, "RFB message limit");
            if p.len() < n {
                break;
            }
            if self.handshake == 0 {
                ensure!(&p[..n] == b"RFB 003.008\n", "RFB 3.8 required");
            }
            if self.handshake == 1 {
                ensure!(
                    p[0] == 1,
                    "Private upstream must use RFB None authentication"
                );
            }
            let clipboard_caps = self.handshake == 3
                && p[0] == 6
                && n >= 12
                && i32::from_be_bytes(p[4..8].try_into()?) < 0
                && u32::from_be_bytes(p[8..12].try_into()?) & 0x1f000000 == 0x01000000;
            if self.handshake < 3
                || !matches!(p[0], 4..=6 | 255)
                || clipboard_caps
                || (control && (p[0] != 6 || clipboard))
            {
                if self.handshake == 3 && p[0] == 4 {
                    let key = u32::from_be_bytes(p[4..8].try_into()?);
                    if p[1] != 0 {
                        self.keys.insert(key);
                    } else {
                        self.keys.remove(&key);
                    }
                }
                if self.handshake == 3 && p[0] == 5 {
                    self.pointer = Some(p[2..6].try_into()?);
                }
                if self.handshake == 3 && p[0] == 255 {
                    let code = p[8..12].try_into()?;
                    if p[2..4] != [0, 0] {
                        self.qkeys.insert(code, p[..n].to_vec());
                    } else {
                        self.qkeys.remove(&code);
                    }
                }
                out.extend_from_slice(&p[..n]);
            }
            self.pending.drain(..n);
            if self.handshake < 3 {
                self.handshake += 1;
            }
        }
        Ok(out)
    }
    pub fn release_input(&mut self) -> Vec<u8> {
        let mut out = Vec::new();
        if self.handshake == 3 {
            for key in std::mem::take(&mut self.keys) {
                out.extend_from_slice(&[4, 0, 0, 0]);
                out.extend_from_slice(&key.to_be_bytes());
            }
            for mut packet in std::mem::take(&mut self.qkeys).into_values() {
                packet[2] = 0;
                packet[3] = 0;
                out.extend_from_slice(&packet);
            }
            if let Some(pointer) = self.pointer.take() {
                out.extend_from_slice(&[5, 0]);
                out.extend_from_slice(&pointer);
            }
        }
        out
    }
    pub fn control_changed(&mut self) -> Result<()> {
        ensure!(
            self.pending.is_empty(),
            "Control changed during partial RFB packet; reconnect"
        );
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn fragmented_input_and_clipboard_are_gated() {
        let mut gate = RfbGate::default();
        assert_eq!(
            gate.push(b"RFB 003.008\n\x01\x01", false, false)
                .unwrap()
                .len(),
            14
        );
        assert!(gate.push(&[4, 1, 0], true, true).unwrap().is_empty());
        assert!(gate.control_changed().is_err());
        assert!(
            gate.push(&[0, 0, 0, 0, 65], false, false)
                .unwrap()
                .is_empty()
        );
        assert_eq!(
            gate.push(&[4, 0, 0, 0, 0, 0, 0, 65], true, false)
                .unwrap()
                .len(),
            8
        );
        assert!(
            gate.push(&[6, 0, 0, 0, 0, 0, 0, 1, 65], true, false)
                .unwrap()
                .is_empty()
        );
        assert!(gate.push(&[254], true, true).is_err());
    }
}
