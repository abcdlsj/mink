/// Replace characters that are unsafe in file names and cap the length.
pub fn sanitize_file_name(value: &str) -> String {
    let mut sanitized = value
        .chars()
        .map(|character| {
            if character.is_control()
                || matches!(
                    character,
                    '/' | '\\' | ':' | '*' | '?' | '"' | '<' | '>' | '|'
                )
            {
                '_'
            } else {
                character
            }
        })
        .collect::<String>();
    let trimmed = sanitized.trim().trim_start_matches(['.', '_']);
    if trimmed.is_empty() {
        return "attachment".into();
    }
    sanitized = trimmed.to_owned();
    if sanitized.chars().count() > 120 {
        let mut boundary = 120;
        while !sanitized.is_char_boundary(boundary) {
            boundary -= 1;
        }
        sanitized.truncate(boundary);
    }
    sanitized
}

/// Split a reply into Telegram-sized chunks, preferring newline boundaries.
pub fn split_reply(text: &str, max_chars: usize) -> Vec<String> {
    let mut parts = Vec::new();
    let mut remaining = text;
    while !remaining.is_empty() {
        let mut boundary = remaining
            .char_indices()
            .take_while(|(index, _)| *index < max_chars)
            .map(|(index, character)| index + character.len_utf8())
            .last()
            .unwrap_or(0);
        if boundary == remaining.len() {
            parts.push(remaining.to_owned());
            break;
        }
        if let Some(newline) = remaining[..boundary].rfind('\n')
            && newline > 0
        {
            boundary = newline;
        }
        parts.push(remaining[..boundary].trim_end().to_owned());
        remaining = remaining[boundary..].trim_start();
    }
    parts
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn file_names_are_sanitized_and_bounded() {
        assert_eq!(sanitize_file_name("../../etc/passwd"), "etc_passwd");
        assert_eq!(
            sanitize_file_name("report: final?.pdf"),
            "report_ final_.pdf"
        );
        assert_eq!(sanitize_file_name(".."), "attachment");
        assert_eq!(sanitize_file_name(""), "attachment");
        assert!(sanitize_file_name(&"a".repeat(500)).chars().count() <= 120);
        assert_eq!(sanitize_file_name("  clean.pdf  "), "clean.pdf");
    }

    #[test]
    fn replies_split_at_newline_boundaries() {
        let parts = split_reply("line one\nline two\nline three", 14);
        assert_eq!(parts, vec!["line one", "line two", "line three"]);
    }

    #[test]
    fn long_words_are_hard_split() {
        let parts = split_reply(&"x".repeat(10), 4);
        assert_eq!(parts, vec!["xxxx", "xxxx", "xx"]);
    }
}
