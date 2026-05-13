package telegram

import (
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/abcdlsj/sumi/textutil"
	"github.com/mattn/go-runewidth"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

const (
	telegramTableWidth = 72
	telegramCellMin    = 4
	telegramCellMax    = 36
	telegramCellGap    = 2
	telegramCellSep    = "  "
)

var telegramGoldmark = goldmark.New(goldmark.WithExtensions(extension.GFM))

type telegramMarkdownRenderer struct {
	src []byte
	b   strings.Builder
}

func renderTelegramHTML(s string) string {
	src := []byte(textutil.Valid(s))
	doc := telegramGoldmark.Parser().Parse(text.NewReader(src))
	r := telegramMarkdownRenderer{src: src}
	r.blocks(doc.FirstChild())
	return strings.TrimSpace(r.b.String())
}

func (r *telegramMarkdownRenderer) blocks(n ast.Node) {
	for ; n != nil; n = n.NextSibling() {
		r.block(n)
	}
}

func (r *telegramMarkdownRenderer) block(n ast.Node) {
	switch n := n.(type) {
	case *ast.Heading:
		r.gap()
		r.b.WriteString("<b>")
		r.inlines(n.FirstChild())
		r.b.WriteString("</b>")
	case *ast.Paragraph, *ast.TextBlock:
		r.gap()
		r.inlines(n.FirstChild())
	case *ast.Blockquote:
		r.gap()
		r.b.WriteString("<blockquote>")
		r.b.WriteString(r.renderBlocks(n.FirstChild()))
		r.b.WriteString("</blockquote>")
	case *ast.CodeBlock:
		r.code("", n.Lines().Value(r.src))
	case *ast.FencedCodeBlock:
		r.code(string(n.Language(r.src)), n.Lines().Value(r.src))
	case *ast.List:
		r.list(n)
	case *ast.ThematicBreak:
		r.gap()
		r.b.WriteString("---")
	case *east.Table:
		r.table(n)
	case *ast.HTMLBlock:
		r.gap()
		r.b.WriteString(esc(string(n.Text(r.src))))
	default:
		if n.HasChildren() {
			r.blocks(n.FirstChild())
		}
	}
}

func (r *telegramMarkdownRenderer) inlines(n ast.Node) {
	for ; n != nil; n = n.NextSibling() {
		r.inline(n)
	}
}

func (r *telegramMarkdownRenderer) inline(n ast.Node) {
	switch n := n.(type) {
	case *ast.Text:
		r.b.WriteString(esc(string(n.Value(r.src))))
		if n.HardLineBreak() {
			r.b.WriteByte('\n')
		} else if n.SoftLineBreak() {
			r.b.WriteByte(' ')
		}
	case *ast.String:
		r.b.WriteString(esc(string(n.Value)))
	case *ast.CodeSpan:
		r.b.WriteString("<code>")
		r.b.WriteString(esc(r.plain(n)))
		r.b.WriteString("</code>")
	case *ast.Emphasis:
		tag := "i"
		if n.Level >= 2 {
			tag = "b"
		}
		r.tag(tag, n)
	case *ast.Link:
		r.link(n.Destination, n)
	case *ast.AutoLink:
		r.b.WriteString(`<a href="`)
		r.b.WriteString(attr(string(n.URL(r.src))))
		r.b.WriteString(`">`)
		r.b.WriteString(esc(string(n.Label(r.src))))
		r.b.WriteString("</a>")
	case *ast.Image:
		r.inlines(n.FirstChild())
	case *ast.RawHTML:
		r.b.WriteString(esc(string(n.Text(r.src))))
	default:
		switch n.Kind() {
		case east.KindStrikethrough:
			r.tag("s", n)
		case east.KindTaskCheckBox:
			if cb, ok := n.(*east.TaskCheckBox); ok && cb.IsChecked {
				r.b.WriteString("[x] ")
			} else {
				r.b.WriteString("[ ] ")
			}
		default:
			if n.HasChildren() {
				r.inlines(n.FirstChild())
			}
		}
	}
}

func (r *telegramMarkdownRenderer) link(dst []byte, n ast.Node) {
	u := strings.TrimSpace(string(dst))
	if !telegramURL(u) {
		r.inlines(n.FirstChild())
		if u != "" {
			r.b.WriteString(" (")
			r.b.WriteString(esc(u))
			r.b.WriteByte(')')
		}
		return
	}
	r.b.WriteString(`<a href="`)
	r.b.WriteString(attr(u))
	r.b.WriteString(`">`)
	r.inlines(n.FirstChild())
	r.b.WriteString("</a>")
}

func (r *telegramMarkdownRenderer) tag(tag string, n ast.Node) {
	r.b.WriteByte('<')
	r.b.WriteString(tag)
	r.b.WriteByte('>')
	r.inlines(n.FirstChild())
	r.b.WriteString("</")
	r.b.WriteString(tag)
	r.b.WriteByte('>')
}

func (r *telegramMarkdownRenderer) code(lang string, body []byte) {
	r.gap()
	r.b.WriteString("<pre>")
	if lang != "" {
		r.b.WriteString(`<code class="language-`)
		r.b.WriteString(attr(lang))
		r.b.WriteString(`">`)
	}
	r.b.WriteString(esc(strings.TrimRight(string(body), "\n")))
	if lang != "" {
		r.b.WriteString("</code>")
	}
	r.b.WriteString("</pre>")
}

func (r *telegramMarkdownRenderer) list(n *ast.List) {
	r.gap()
	i := n.Start
	if i == 0 {
		i = 1
	}
	first := true
	for item := n.FirstChild(); item != nil; item = item.NextSibling() {
		if !first {
			r.b.WriteByte('\n')
		}
		if n.IsOrdered() {
			r.b.WriteString(esc(intLabel(i)))
			i++
		} else {
			r.b.WriteString("- ")
		}
		r.item(item)
		first = false
	}
}

func (r *telegramMarkdownRenderer) item(n ast.Node) {
	first := true
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if p, ok := c.(*ast.Paragraph); ok {
			if !first {
				r.b.WriteByte('\n')
			}
			r.inlines(p.FirstChild())
		} else {
			if !first {
				r.b.WriteByte('\n')
			}
			r.b.WriteString(r.renderBlock(c))
		}
		first = false
	}
}

func (r *telegramMarkdownRenderer) table(n *east.Table) {
	var rows [][]string
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cols []string
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cols = append(cols, textutil.CollapseSpace(r.plain(cell)))
		}
		rows = append(rows, cols)
	}
	if len(rows) == 0 {
		return
	}
	r.gap()
	r.b.WriteString("<pre>")
	r.b.WriteString(esc(renderTelegramTable(rows)))
	r.b.WriteString("</pre>")
}

func (r *telegramMarkdownRenderer) plain(n ast.Node) string {
	var b strings.Builder
	r.plainNode(&b, n)
	return strings.TrimSpace(b.String())
}

func (r *telegramMarkdownRenderer) plainNode(b *strings.Builder, n ast.Node) {
	switch n := n.(type) {
	case *ast.Text:
		b.Write(n.Value(r.src))
		if n.HardLineBreak() || n.SoftLineBreak() {
			b.WriteByte(' ')
		}
	case *ast.String:
		b.Write(n.Value)
	case *ast.CodeBlock:
		b.Write(n.Lines().Value(r.src))
	case *ast.FencedCodeBlock:
		b.Write(n.Lines().Value(r.src))
	case *ast.AutoLink:
		b.Write(n.Label(r.src))
	case *ast.Link:
		if n.HasChildren() {
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				r.plainNode(b, c)
			}
			return
		}
		b.Write(n.Destination)
	default:
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			r.plainNode(b, c)
		}
	}
}

func (r *telegramMarkdownRenderer) renderBlock(n ast.Node) string {
	rr := telegramMarkdownRenderer{src: r.src}
	rr.block(n)
	return strings.TrimSpace(rr.b.String())
}

func (r *telegramMarkdownRenderer) renderBlocks(n ast.Node) string {
	rr := telegramMarkdownRenderer{src: r.src}
	rr.blocks(n)
	return strings.TrimSpace(rr.b.String())
}

func (r *telegramMarkdownRenderer) gap() {
	if r.b.Len() == 0 {
		return
	}
	s := r.b.String()
	if strings.HasSuffix(s, "\n\n") {
		return
	}
	if strings.HasSuffix(s, "\n") {
		r.b.WriteByte('\n')
		return
	}
	r.b.WriteString("\n\n")
}

func renderTelegramTable(rows [][]string) string {
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	widths := telegramTableWidths(rows, cols)
	var out []string
	out = append(out, telegramTableRow(rows[0], widths)...)
	out = append(out, telegramTableRule(widths))
	for _, row := range rows[1:] {
		out = append(out, telegramTableRow(row, widths)...)
	}
	return strings.Join(out, "\n")
}

func telegramTableWidths(rows [][]string, cols int) []int {
	widths := make([]int, cols)
	for _, row := range rows {
		for i := 0; i < cols; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			if w := runewidth.StringWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	for i, w := range widths {
		widths[i] = min(max(w, telegramCellMin), telegramCellMax)
	}
	available := max(cols*telegramCellMin, telegramTableWidth-(cols-1)*telegramCellGap)
	for sum(widths) > available {
		i := widest(widths)
		if widths[i] <= telegramCellMin {
			break
		}
		widths[i]--
	}
	return widths
}

func telegramTableRow(row []string, widths []int) []string {
	wrapped := make([][]string, len(widths))
	height := 1
	for i, width := range widths {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		wrapped[i] = wrapRunes(cell, width)
		if len(wrapped[i]) > height {
			height = len(wrapped[i])
		}
	}
	out := make([]string, 0, height)
	for line := 0; line < height; line++ {
		var b strings.Builder
		for col, width := range widths {
			if col > 0 {
				b.WriteString(telegramCellSep)
			}
			cell := ""
			if line < len(wrapped[col]) {
				cell = wrapped[col][line]
			}
			b.WriteString(padRunes(cell, width))
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func telegramTableRule(widths []int) string {
	var b strings.Builder
	for i, width := range widths {
		if i > 0 {
			b.WriteString(telegramCellSep)
		}
		b.WriteString(strings.Repeat("-", width))
	}
	return b.String()
}

func wrapRunes(s string, width int) []string {
	if width <= 0 {
		return []string{""}
	}
	var out []string
	for _, line := range strings.Split(textutil.Valid(s), "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		var b strings.Builder
		w := 0
		for _, rr := range line {
			rw := runewidth.RuneWidth(rr)
			if rw <= 0 {
				rw = 1
			}
			if w+rw > width {
				out = append(out, b.String())
				b.Reset()
				w = 0
			}
			b.WriteRune(rr)
			w += rw
		}
		out = append(out, b.String())
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func padRunes(s string, width int) string {
	w := runewidth.StringWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func esc(s string) string {
	return html.EscapeString(s)
}

func attr(s string) string {
	return html.EscapeString(s)
}

func telegramURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return false
	}
	switch u.Scheme {
	case "http", "https", "tg", "mailto":
		return true
	default:
		return false
	}
}

func intLabel(i int) string {
	return strconv.Itoa(i) + ". "
}

func sum(xs []int) int {
	var n int
	for _, x := range xs {
		n += x
	}
	return n
}

func widest(xs []int) int {
	var idx int
	for i, x := range xs {
		if x > xs[idx] {
			idx = i
		}
	}
	return idx
}
