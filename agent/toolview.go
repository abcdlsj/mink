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
	for _, t := range reg.All() {
		r = append(r, compactTool{name: t.Name(), desc: t.Desc()})
	}
	if ext != nil {
		for _, t := range ext.Tools() {
			r = append(r, compactTool{name: t.Name(), desc: t.Desc()})
		}
	}
	return r
}

func (v *toolView) tools(reg *tool.Registry, ext *ExtensionManager) []llm.Tool {
	var r []llm.Tool

	for _, t := range reg.All() {
		if v.isExpanded(t.Name()) {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDefinition{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  t.Schema(),
				},
			})
		} else {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDefinition{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  map[string]any{"type": "object"},
				},
			})
		}
	}

	if ext != nil {
		for _, t := range ext.Tools() {
			if v.isExpanded(t.Name()) {
				r = append(r, llm.Tool{
					Type: "function",
					Function: &llm.FunctionDefinition{
						Name:        t.Name(),
						Description: t.Desc(),
						Parameters:  t.Schema(),
					},
				})
			} else {
				r = append(r, llm.Tool{
					Type: "function",
					Function: &llm.FunctionDefinition{
						Name:        t.Name(),
						Description: t.Desc(),
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

func toolDetail(t *tool.Tool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("<tool name=\"%s\">\n", t.Name()))
	b.WriteString(fmt.Sprintf("  %s\n", t.Desc()))
	b.WriteString("</tool>")
	return b.String()
}
