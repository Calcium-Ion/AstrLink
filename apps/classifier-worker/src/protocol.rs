use std::io::{self, Read, Write};

use serde::{Deserialize, Serialize};

pub const PROTOCOL_VERSION: u8 = 1;
const MAX_FRAME_BYTES: usize = 64 * 1024 * 1024;

#[derive(Debug, Serialize)]
struct ReadyResponse {
    version: u8,
    ready: bool,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ClassifyRequest {
    pub version: u8,
    pub id: u64,
    pub text: String,
}

#[derive(Debug, Serialize)]
pub struct ClassifyResponse {
    pub version: u8,
    pub id: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub category: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub logits: Option<Vec<f32>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<ProtocolError>,
}

impl ClassifyResponse {
    pub fn success(id: u64, category: String, logits: Vec<f32>) -> Self {
        Self {
            version: PROTOCOL_VERSION,
            id,
            category: Some(category),
            logits: Some(logits),
            error: None,
        }
    }

    pub fn failure(id: u64, code: &'static str) -> Self {
        Self {
            version: PROTOCOL_VERSION,
            id,
            category: None,
            logits: None,
            error: Some(ProtocolError { code }),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct ProtocolError {
    pub code: &'static str,
}

pub fn read_request(reader: &mut impl Read) -> io::Result<Option<ClassifyRequest>> {
    let Some(payload) = read_frame(reader)? else {
        return Ok(None);
    };
    serde_json::from_slice(&payload)
        .map(Some)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid request"))
}

pub fn write_response(writer: &mut impl Write, response: &ClassifyResponse) -> io::Result<()> {
    let payload = serde_json::to_vec(response)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid response"))?;
    write_frame(writer, &payload)
}

pub fn write_ready(writer: &mut impl Write) -> io::Result<()> {
    let payload = serde_json::to_vec(&ReadyResponse {
        version: PROTOCOL_VERSION,
        ready: true,
    })
    .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid ready response"))?;
    write_frame(writer, &payload)
}

fn read_frame(reader: &mut impl Read) -> io::Result<Option<Vec<u8>>> {
    let mut header = [0_u8; 4];
    let bytes_read = reader.read(&mut header[..1])?;
    if bytes_read == 0 {
        return Ok(None);
    }
    reader.read_exact(&mut header[1..])?;
    let length = u32::from_be_bytes(header) as usize;
    if length == 0 || length > MAX_FRAME_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid frame length",
        ));
    }
    let mut payload = vec![0_u8; length];
    reader.read_exact(&mut payload)?;
    Ok(Some(payload))
}

fn write_frame(writer: &mut impl Write, payload: &[u8]) -> io::Result<()> {
    let length = u32::try_from(payload.len())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "response too large"))?;
    if payload.is_empty() || payload.len() > MAX_FRAME_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid response length",
        ));
    }
    writer.write_all(&length.to_be_bytes())?;
    writer.write_all(payload)?;
    writer.flush()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frame_round_trip_does_not_echo_request_text() {
        let request = br#"{"version":1,"id":7,"text":"secret user prompt"}"#;
        let mut encoded = Vec::new();
        write_frame(&mut encoded, request).expect("write request");
        let parsed = read_request(&mut encoded.as_slice())
            .expect("read request")
            .expect("request");
        assert_eq!(parsed.id, 7);
        assert_eq!(parsed.text, "secret user prompt");

        let response = ClassifyResponse::success(7, "general".into(), vec![1.0, 0.0, 0.0, 0.0]);
        let mut response_bytes = Vec::new();
        write_response(&mut response_bytes, &response).expect("write response");
        assert!(!String::from_utf8_lossy(&response_bytes).contains("secret user prompt"));
    }

    #[test]
    fn ready_handshake_is_framed_and_versioned() {
        let mut encoded = Vec::new();
        write_ready(&mut encoded).expect("write ready");
        let payload = read_frame(&mut encoded.as_slice())
            .expect("read ready")
            .expect("ready frame");
        assert_eq!(
            serde_json::from_slice::<serde_json::Value>(&payload).expect("ready JSON"),
            serde_json::json!({"version": PROTOCOL_VERSION, "ready": true})
        );
    }

    #[test]
    fn rejects_oversized_frame_before_allocating_payload() {
        let header = ((MAX_FRAME_BYTES + 1) as u32).to_be_bytes();
        let error = read_request(&mut header.as_slice()).expect_err("must reject");
        assert_eq!(error.kind(), io::ErrorKind::InvalidData);
    }
}
