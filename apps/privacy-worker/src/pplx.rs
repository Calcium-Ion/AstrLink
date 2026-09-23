use std::sync::OnceLock;

use regex::Regex;

use crate::protocol::DetectedSpan;

// The byte-level tokenizer can attach a delimiter to a predicted token, or
// leave a prefix outside a span. Repair only complete, recognizable values;
// this never creates a new detection or suppresses a model finding.
pub fn normalize_boundaries(text: &str, spans: &mut [DetectedSpan]) {
    let bytes = text.as_bytes();
    for span in spans {
        let (mut start, mut end) = (span.start, span.end);
        if start >= end || text.get(start..end).is_none() {
            continue;
        }
        if span.label == "common_secret"
            && let Some(value) = quoted_assignment_value(text, start, end)
        {
            (start, end) = value;
        }
        for quote in *b"\"'`" {
            if start >= end {
                break;
            }
            if bytes[start] == quote && (bytes[end - 1] == quote || bytes.get(end) == Some(&quote))
            {
                start += 1;
            }
            if start < end && bytes[end - 1] == quote && start > 0 && bytes[start - 1] == quote {
                end -= 1;
            }
        }
        if start >= end {
            continue;
        }
        let (expanded_start, expanded_end) = match span.label.as_str() {
            "email" => expand(bytes, start, end, email_character),
            "common_secret" if bytes[start..end].iter().all(|byte| token_character(*byte)) => {
                expand(bytes, start, end, token_character)
            }
            "private_date" => expand(bytes, start, end, |byte| {
                byte.is_ascii_digit() || byte == b'-'
            }),
            "phone" if bytes[start..end].iter().all(u8::is_ascii_digit) => {
                expand(bytes, start, end, |byte| byte.is_ascii_digit())
            }
            _ => (start, end),
        };
        let candidate = &text[expanded_start..expanded_end];
        let valid = match span.label.as_str() {
            "email" => email_pattern().is_match(candidate),
            "private_date" => iso_date(candidate),
            "common_secret" => true,
            "phone" => {
                (7..=15).contains(&candidate.len())
                    && candidate.bytes().all(|byte| byte.is_ascii_digit())
            }
            _ => false,
        };
        if valid {
            start = expanded_start;
            end = expanded_end;
        }
        span.start = start;
        span.end = end;
    }
}

// A model can include the assignment key in a quoted secret span. Narrow only
// when the prediction covers the complete literal and ends at its closing quote.
// Escaped or multiline strings require a language parser and are left intact.
fn quoted_assignment_value(text: &str, start: usize, end: usize) -> Option<(usize, usize)> {
    static PREFIX: OnceLock<Regex> = OnceLock::new();
    let prefix = PREFIX.get_or_init(|| {
        Regex::new(r#"^[A-Za-z_][A-Za-z_0-9]*[ \t]*=[ \t]*["']"#)
            .expect("quoted assignment boundary pattern")
    });
    let matched = prefix.find(&text[start..])?;
    let value_start = start + matched.end();
    let quote = text.as_bytes()[value_start - 1];
    let remainder = &text.as_bytes()[value_start..];
    let length = remainder.iter().position(|byte| *byte == quote)?;
    let value_end = value_start + length;
    if length == 0
        || remainder[..length]
            .iter()
            .any(|byte| matches!(byte, b'\\' | b'\n' | b'\r'))
        || (end != value_end && end != value_end + 1)
    {
        return None;
    }
    Some((value_start, value_end))
}

fn expand(
    bytes: &[u8],
    mut start: usize,
    mut end: usize,
    allowed: fn(u8) -> bool,
) -> (usize, usize) {
    while start > 0 && allowed(bytes[start - 1]) {
        start -= 1;
    }
    while end < bytes.len() && allowed(bytes[end]) {
        end += 1;
    }
    (start, end)
}

fn token_character(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-')
}

fn email_character(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || matches!(byte, b'@' | b'.' | b'_' | b'%' | b'+' | b'-')
}

fn email_pattern() -> &'static Regex {
    static PATTERN: OnceLock<Regex> = OnceLock::new();
    PATTERN.get_or_init(|| {
        Regex::new(r"^[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,63}$")
            .expect("email boundary pattern")
    })
}

fn iso_date(value: &str) -> bool {
    let bytes = value.as_bytes();
    bytes.len() == 10
        && bytes[4] == b'-'
        && bytes[7] == b'-'
        && bytes
            .iter()
            .enumerate()
            .all(|(index, byte)| index == 4 || index == 7 || byte.is_ascii_digit())
        && ("01"..="12").contains(&&value[5..7])
        && ("01"..="31").contains(&&value[8..10])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn repairs_recognizable_values_without_consuming_code_quotes() {
        for (label, text, predicted, expected) in [
            (
                "email",
                "// contact daniels@meridiancap.com",
                "aniels@meridiancap.com",
                "daniels@meridiancap.com",
            ),
            (
                "email",
                "邮箱 zhang.wei@northwind-mail.net",
                "hang.wei@northwind-mail.net",
                "zhang.wei@northwind-mail.net",
            ),
            (
                "common_secret",
                "apiKey = \"sk-proj-abcdef0123456789\";",
                "abcdef0123456789",
                "sk-proj-abcdef0123456789",
            ),
            (
                "common_secret",
                "PASSWORD=\"r7Q!m2V#k9L@x4T\"",
                "r7Q!m2V#k9L@x4T\"",
                "r7Q!m2V#k9L@x4T",
            ),
            (
                "common_secret",
                "DB_PASS='K8w!R3p#N6t'",
                "DB_PASS='K8w!R3p#N6t",
                "K8w!R3p#N6t",
            ),
            (
                "common_secret",
                "DB_PASS='K8w!R3p#N6t'",
                "DB_PASS='K8w!R3p",
                "DB_PASS='K8w!R3p",
            ),
            (
                "common_secret",
                "DB_PASS='ab\\'cd'",
                "DB_PASS='ab\\'cd'",
                "DB_PASS='ab\\'cd'",
            ),
            (
                "phone",
                "联系电话：13987654321。",
                "3987654321",
                "13987654321",
            ),
            (
                "phone",
                "Account: 1234567890123456",
                "234567890123456",
                "234567890123456",
            ),
            (
                "phone",
                "{\"phone\": \"+1-415-555-0198\"}",
                "\"+1-415-555-0198",
                "+1-415-555-0198",
            ),
            (
                "private_date",
                "Birth date: 1987-06-15",
                "987-06-15",
                "1987-06-15",
            ),
            (
                "private_date",
                "Version: 2020-1987-06-15",
                "1987-06-15",
                "1987-06-15",
            ),
            ("private_person", "Customer O'Brian", "O'Brian", "O'Brian"),
            ("common_secret", "\"", "\"", "\""),
            (
                "common_secret",
                "checksum = \"e3b0c44298fc1c149afbf4c8996fb92427\"",
                "e3b0c44298fc1c149afbf4c8996fb92427",
                "e3b0c44298fc1c149afbf4c8996fb92427",
            ),
        ] {
            let start = text.find(predicted).expect("prediction");
            let mut spans = [DetectedSpan {
                text_id: 0,
                label: label.into(),
                start,
                end: start + predicted.len(),
                score: 0.9,
            }];
            normalize_boundaries(text, &mut spans);
            assert_eq!(&text[spans[0].start..spans[0].end], expected, "{text}");
            assert_eq!(spans[0].score, 0.9);
        }
    }
}
