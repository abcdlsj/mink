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
}
