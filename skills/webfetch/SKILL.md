---
name: webfetch
description: Fetch web page content and convert to markdown using r.jina.ai
---

# WebFetch Skill

Extract clean markdown content from any webpage using r.jina.ai service.

## Usage

Use the `webfetch` tool (or bash with curl) to fetch webpage content:

```bash
!curl -s "https://r.jina.ai/http://example.com/article"
```

Or with the webfetch tool:

```json
{"url": "https://example.com/article"}
```

## Examples

### Fetch Documentation

```bash
!curl -s "https://r.jina.ai/http://docs.example.com/api"
```

### Extract Article Content

```bash
!curl -s "https://r.jina.ai/http://blog.example.com/post"
```

### Fetch with Query Parameters

```bash
!curl -s "https://r.jina.ai/http://api.example.com/docs?page=1"
```

## Tips

- Works best on article pages and documentation
- Removes ads, navigation, and clutter
- Returns clean markdown format
- Supports most websites
- Rate limit: be respectful, don't abuse

## Common Use Cases

1. **Research**: Fetch articles for analysis
2. **Documentation**: Extract API docs
3. **Comparison**: Compare content from multiple sources
4. **Archival**: Save clean versions of web pages
