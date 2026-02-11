package agent

import (
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/tool"
)

type toolView struct {
	expanded map[string]bool
}

func newToolView() *toolView {
	return &toolView{expanded: make(map[string]bool)}
}

func (v *toolView) expand(name string) {
	v.expanded[name] = true
}

func (v *toolView) tools(reg *tool.Registry) []llm.Tool {
	var r []llm.Tool
	for _, t := range reg.All() {
		if v.expanded[t.Name()] {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  t.Schema(),
				},
			})
		} else {
			r = append(r, llm.Tool{
				Type: "function",
				Function: &llm.FunctionDef{
					Name:        t.Name(),
					Description: t.Desc(),
					Parameters:  map[string]any{"type": "object"},
				},
			})
		}
	}
	return r
}
