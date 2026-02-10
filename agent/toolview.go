package agent

import (
	"fmt"
	"strings"

	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/tool"
)

// toolView implements progressive tool exposure
type toolView struct {
	expanded map[string]bool
}

func newToolView() *toolView {
	return &toolView{expanded: make(map[string]bool)}
}

func (v *toolView) reset() {
	v.expanded = make(map[string]bool)
}

func (v *toolView) expand(name string) {
	v.expanded[name] = true
}

func (v *toolView) isExpanded(name string) bool {
	return v.expanded[name]
}

type compactTool struct {
	name string
	desc string
}

func (v *toolView) compact(reg *tool.Registry, ext *ExtensionManager) []compactTool {
	var r []compactTool
	for _, tl := range reg.All() {
		r = append(r, compactTool{name: tl.Name(), desc: tl.Desc()})
	}
	if ext != nil {
		for _, et := range ext.Tools() {
			r = append(r, compactTool{name: et.Name, desc: et.Desc})
		}
	}
	return r
}

func (v *toolView) tools(reg *tool.Registry, ext *ExtensionManager) []llm.Tool {
	var r []llm.Tool

	for _, tl := range reg.All() {
		if v.isExpanded(tl.Name()) {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        tl.Name(),
					Description: tl.Desc(),
					Parameters:  tl.Schema(),
				},
			})
		} else {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        tl.Name(),
					Description: tl.Desc(),
					Parameters:  map[string]any{"type": "object"},
				},
			})
		}
	}

	if ext != nil {
		for _, et := range ext.Tools() {
			if v.isExpanded(et.Name) {
				r = append(r, llm.Tool{
					Type: "function",
					Function: &llm.FunctionDef{
						Name:        et.Name,
						Description: et.Desc,
						Parameters:  et.Schema,
					},
				})
			} else {
				r = append(r, llm.Tool{
					Type: "function",
					Function: &llm.FunctionDef{
						Name:        et.Name,
						Description: et.Desc,
						Parameters:  map[string]any{"type": "object"},
					},
				})
			}
		}
	}

	return r
}

func (v *toolView) expandFromHint(s string) {
	// extract $toolname from text
	words := strings.Fields(s)
	for _, w := range words {
		if strings.HasPrefix(w, "$") {
			v.expand(strings.TrimPrefix(w, "$"))
		}
	}
}

func toolDetail(t tool.Tool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("<tool name=\"%s\">\n", t.Name()))
	b.WriteString(fmt.Sprintf("  %s\n", t.Desc()))
	b.WriteString("</tool>")
	return b.String()
}
