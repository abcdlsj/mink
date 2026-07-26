pub fn is_slug(value: &str, min_len: usize, max_len: usize) -> bool {
    (min_len..=max_len).contains(&value.len())
        && !value.starts_with('-')
        && !value.ends_with('-')
        && !value.contains("--")
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'-')
}

#[cfg(test)]
mod tests {
    use super::is_slug;

    #[test]
    fn slug_grammar_and_length_are_enforced_once() {
        assert!(is_slug("x", 1, 32));
        assert!(is_slug("sumi-lab", 3, 32));
        assert!(!is_slug("ab", 3, 32));
        assert!(!is_slug("Design", 1, 32));
        assert!(!is_slug("two--parts", 1, 32));
        assert!(!is_slug("-leading", 1, 32));
        assert!(!is_slug("trailing-", 1, 32));
    }
}
