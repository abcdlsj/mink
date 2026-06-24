package app

import "github.com/abcdlsj/sumi/persona"

func memoryToolBlocks(p *persona.Persona) map[string]string {
	if p != nil && p.MemoryPolicy == "auto_commit" {
		return nil
	}
	reason := "current memory policy is proposal-only; long-term memory changes require human confirmation"
	return map[string]string{
		"write_memory":  reason,
		"update_memory": reason,
		"delete_memory": reason,
	}
}

func mergeToolBlocks(groups ...map[string]string) map[string]string {
	var out map[string]string
	for _, group := range groups {
		for name, reason := range group {
			if out == nil {
				out = map[string]string{}
			}
			out[name] = reason
		}
	}
	return out
}
