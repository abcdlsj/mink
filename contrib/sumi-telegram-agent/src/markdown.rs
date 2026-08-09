use pulldown_cmark::{Event, Options, Parser, Tag, TagEnd};

/// Telegram counts message length in UTF-16 code units; keeping the byte budget
/// below 4000 leaves a safe margin for multi-byte characters and entities.
pub const MAX_MESSAGE_CHARS: usize = 4000;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ReplyImage {
    pub alt: String,
    pub url: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct RenderedReply {
    pub messages: Vec<String>,
    pub images: Vec<ReplyImage>,
}

/// Render a CommonMark subset into Telegram-HTML blocks and collect inline
/// images. Blocks are grouped into messages that fit Telegram's length limit.
pub fn render_markdown(input: &str) -> RenderedReply {
    let mut options = Options::empty();
    options.insert(Options::ENABLE_STRIKETHROUGH);
    options.insert(Options::ENABLE_TASKLISTS);
    let parser = Parser::new_ext(input, options);

    let mut blocks = Vec::new();
    let mut current = String::new();
    let mut images = Vec::new();
    let mut pending_image = None;
    let mut ordered_list = false;
    let mut list_index = 0_u64;

    for event in parser {
        match event {
            Event::Start(tag) => match tag {
                Tag::Emphasis => current.push_str("<i>"),
                Tag::Strong => current.push_str("<b>"),
                Tag::Strikethrough => current.push_str("<s>"),
                Tag::Link { dest_url, .. } => {
                    current.push_str(&format!("<a href=\"{}\">", escape_html(&dest_url)));
                }
                Tag::Image { dest_url, .. } => {
                    pending_image = Some(PendingImage {
                        url: dest_url.to_string(),
                        alt: String::new(),
                    });
                }
                Tag::Heading { .. } => {
                    flush(&mut blocks, &mut current);
                    current.push_str("<b>");
                }
                Tag::BlockQuote(_) => {
                    flush(&mut blocks, &mut current);
                    current.push_str("<blockquote>");
                }
                Tag::CodeBlock(_) => {
                    flush(&mut blocks, &mut current);
                    current.push_str("<pre>");
                }
                Tag::List(ordered) => {
                    flush(&mut blocks, &mut current);
                    ordered_list = ordered.is_some();
                    list_index = 0;
                }
                Tag::Item => {
                    flush(&mut blocks, &mut current);
                    if ordered_list {
                        list_index += 1;
                        current.push_str(&format!("{list_index}. "));
                    } else {
                        current.push_str("- ");
                    }
                }
                _ => {}
            },
            Event::End(tag_end) => match tag_end {
                TagEnd::Paragraph => flush(&mut blocks, &mut current),
                TagEnd::Emphasis => current.push_str("</i>"),
                TagEnd::Strong => current.push_str("</b>"),
                TagEnd::Strikethrough => current.push_str("</s>"),
                TagEnd::Link => current.push_str("</a>"),
                TagEnd::Image => {
                    if let Some(image) = pending_image.take() {
                        let label = if image.alt.is_empty() {
                            "image".to_owned()
                        } else {
                            image.alt.clone()
                        };
                        images.push(ReplyImage {
                            alt: image.alt,
                            url: image.url,
                        });
                        current.push_str(&format!("<i>[{}]</i>", escape_html(&label)));
                    }
                }
                TagEnd::Heading(_) => {
                    current.push_str("</b>");
                    flush(&mut blocks, &mut current);
                }
                TagEnd::BlockQuote(_) => {
                    current.push_str("</blockquote>");
                    flush(&mut blocks, &mut current);
                }
                TagEnd::CodeBlock => {
                    while current.ends_with('\n') {
                        current.pop();
                    }
                    current.push_str("</pre>");
                    flush(&mut blocks, &mut current);
                }
                TagEnd::List(_) | TagEnd::Item => flush(&mut blocks, &mut current),
                _ => {}
            },
            Event::Text(text) => {
                if let Some(image) = &mut pending_image {
                    image.alt.push_str(&text);
                } else {
                    current.push_str(&escape_html(&text));
                }
            }
            Event::Code(text) => {
                current.push_str(&format!("<code>{}</code>", escape_html(&text)));
            }
            Event::SoftBreak | Event::HardBreak => current.push('\n'),
            Event::Rule => {
                flush(&mut blocks, &mut current);
                blocks.push("─".repeat(12));
            }
            Event::TaskListMarker(checked) => {
                current.push_str(if checked { "[x] " } else { "[ ] " });
            }
            Event::Html(html) | Event::InlineHtml(html) => {
                current.push_str(&escape_html(&html));
            }
            Event::FootnoteReference(name) => {
                current.push_str(&format!("[{}]", name));
            }
            _ => {}
        }
    }
    flush(&mut blocks, &mut current);

    let mut messages = Vec::new();
    let mut message = String::new();
    for block in blocks {
        if block.trim().is_empty() {
            continue;
        }
        let fits = message.is_empty()
            || message.chars().count() + 1 + block.chars().count() <= MAX_MESSAGE_CHARS;
        if !fits {
            messages.push(std::mem::take(&mut message));
        }
        if block.chars().count() > MAX_MESSAGE_CHARS {
            messages.extend(split_at_chars(&block, MAX_MESSAGE_CHARS));
            continue;
        }
        if !message.is_empty() {
            message.push('\n');
        }
        message.push_str(&block);
    }
    if !message.is_empty() {
        messages.push(message);
    }
    RenderedReply { messages, images }
}

struct PendingImage {
    url: String,
    alt: String,
}

fn flush(blocks: &mut Vec<String>, current: &mut String) {
    if !current.is_empty() {
        blocks.push(std::mem::take(current));
    }
}

fn escape_html(input: &str) -> String {
    let mut output = String::with_capacity(input.len());
    for character in input.chars() {
        match character {
            '&' => output.push_str("&amp;"),
            '<' => output.push_str("&lt;"),
            '>' => output.push_str("&gt;"),
            '"' => output.push_str("&quot;"),
            _ => output.push(character),
        }
    }
    output
}

fn split_at_chars(input: &str, max: usize) -> Vec<String> {
    let mut chunks = Vec::new();
    let mut remaining = input;
    while !remaining.is_empty() {
        let boundary = remaining
            .char_indices()
            .nth(max)
            .map(|(index, _)| index)
            .unwrap_or(remaining.len());
        if boundary == remaining.len() {
            chunks.push(remaining.to_owned());
            break;
        }
        chunks.push(remaining[..boundary].to_owned());
        remaining = &remaining[boundary..];
    }
    chunks
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn renders_inline_styles_links_and_escapes_html() {
        let rendered = render_markdown(
            "**bold** and *italic* and `code` and [link](https://example.com) and <script>x</script>",
        );
        assert_eq!(
            rendered.messages,
            vec![
                "<b>bold</b> and <i>italic</i> and <code>code</code> and \
                 <a href=\"https://example.com\">link</a> and \
                 &lt;script&gt;x&lt;/script&gt;"
            ]
        );
        assert!(rendered.images.is_empty());
    }

    #[test]
    fn collects_remote_and_local_images_with_alt_placeholders() {
        let rendered = render_markdown(
            "See ![chart](https://example.com/chart.png) and ![local](workspace/diagram.png)",
        );
        assert_eq!(
            rendered.images,
            vec![
                ReplyImage {
                    alt: "chart".into(),
                    url: "https://example.com/chart.png".into(),
                },
                ReplyImage {
                    alt: "local".into(),
                    url: "workspace/diagram.png".into(),
                },
            ]
        );
        assert_eq!(
            rendered.messages[0],
            "See <i>[chart]</i> and <i>[local]</i>"
        );
    }

    #[test]
    fn renders_headings_lists_code_blocks_and_tasks() {
        let rendered = render_markdown(
            "# Title\n\n- one\n- two\n\n1. first\n2. second\n\n```rust\nfn main() {}\n```\n\n- [x] done",
        );
        assert_eq!(
            rendered.messages,
            vec![
                "<b>Title</b>\n- one\n- two\n1. first\n2. second\n\
                 <pre>fn main() {}</pre>\n- [x] done",
            ]
        );
    }

    #[test]
    fn long_replies_split_into_bounded_messages() {
        let rendered = render_markdown(&format!("paragraph with **bold**\n\n{}", "x".repeat(8000)));
        assert_eq!(rendered.messages.len(), 3);
        for message in &rendered.messages {
            assert!(message.chars().count() <= MAX_MESSAGE_CHARS);
        }
    }
}
