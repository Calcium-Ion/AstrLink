use unicode_normalization::UnicodeNormalization;

const MAX_UTF8_BYTES: usize = 64 * 1024;
const KEEP_UTF8_BYTES: usize = 32 * 1024;

#[derive(Debug, Eq, PartialEq)]
pub enum NormalizeError {
    Nul,
    Empty,
    InvalidUtf8,
}

impl NormalizeError {
    pub fn code(self) -> &'static str {
        match self {
            Self::Nul | Self::InvalidUtf8 => "invalid_text",
            Self::Empty => "empty_text",
        }
    }
}

/// Frozen `tokenize_head_tail-v1` text normalization.
///
/// Order is part of the contract and must not be rearranged:
/// valid UTF-8, reject NUL, NFC, CRLF to LF, strip, then the 64 KiB
/// head/tail byte window with character-boundary repair.
pub fn normalize_current_user_text(text: &str) -> Result<String, NormalizeError> {
    if text.as_bytes().contains(&0) {
        return Err(NormalizeError::Nul);
    }
    let normalized: String = text.nfc().collect();
    let folded = normalized.replace("\r\n", "\n");
    let stripped = folded.trim();
    if stripped.is_empty() {
        return Err(NormalizeError::Empty);
    }
    let bytes = stripped.as_bytes();
    if bytes.len() <= MAX_UTF8_BYTES {
        return Ok(stripped.to_owned());
    }
    let head = prefix_char_boundary(bytes, KEEP_UTF8_BYTES);
    let tail = suffix_char_boundary(bytes, KEEP_UTF8_BYTES);
    let mut joined = Vec::with_capacity(head.len() + tail.len());
    joined.extend_from_slice(head);
    joined.extend_from_slice(tail);
    String::from_utf8(joined).map_err(|_| NormalizeError::InvalidUtf8)
}

fn prefix_char_boundary(bytes: &[u8], max: usize) -> &[u8] {
    let mut end = max.min(bytes.len());
    while end > 0 && !is_char_boundary(bytes[end]) {
        end -= 1;
    }
    &bytes[..end]
}

fn suffix_char_boundary(bytes: &[u8], max: usize) -> &[u8] {
    if bytes.len() <= max {
        return bytes;
    }
    let mut start = bytes.len() - max;
    while start < bytes.len() && !is_char_boundary(bytes[start]) {
        start += 1;
    }
    &bytes[start..]
}

fn is_char_boundary(byte: u8) -> bool {
    byte as i8 >= -0x40
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_nul_and_empty() {
        assert_eq!(
            normalize_current_user_text("hello\0world"),
            Err(NormalizeError::Nul)
        );
        assert_eq!(
            normalize_current_user_text("   \n"),
            Err(NormalizeError::Empty)
        );
    }

    #[test]
    fn folds_crlf_strips_and_keeps_lone_cr() {
        assert_eq!(
            normalize_current_user_text("  hello\r\nworld  ").expect("normalize"),
            "hello\nworld"
        );
        assert_eq!(
            normalize_current_user_text("keep\rlone").expect("normalize"),
            "keep\rlone"
        );
    }

    #[test]
    fn applies_nfc() {
        let decomposed = "e\u{0301}";
        assert_eq!(
            normalize_current_user_text(decomposed).expect("normalize"),
            "é"
        );
    }

    #[test]
    fn keeps_short_text_and_clips_over_64kib_without_separator() {
        let short = "a".repeat(64 * 1024);
        assert_eq!(
            normalize_current_user_text(&short)
                .expect("keep 64kib")
                .len(),
            64 * 1024
        );

        let mut long = "H".repeat(32 * 1024);
        long.push_str("MIDDLE");
        long.push_str(&"T".repeat(32 * 1024));
        let clipped = normalize_current_user_text(&long).expect("clip");
        assert!(clipped.starts_with('H'));
        assert!(clipped.ends_with('T'));
        assert!(!clipped.contains("MIDDLE"));
        assert_eq!(clipped.len(), 64 * 1024);
    }

    #[test]
    fn repairs_utf8_boundaries_on_the_64kib_window() {
        let snowman = "☃"; // 3 bytes
        let mut long = snowman.repeat((32 * 1024) / 3);
        long.push('X');
        long.push_str(&snowman.repeat((32 * 1024) / 3 + 8));
        assert!(long.len() > 64 * 1024);
        let clipped = normalize_current_user_text(&long).expect("clip");
        assert!(clipped.is_char_boundary(clipped.len()));
        assert!(!clipped.contains('\u{FFFD}'));
    }
}
