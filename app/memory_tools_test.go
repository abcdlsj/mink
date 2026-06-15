package app

import (
	"testing"

	"github.com/abcdlsj/sumi/persona"
)

func TestMemoryToolBlocksDefaultToProposalOnly(t *testing.T) {
	blocks := memoryToolBlocks(&persona.Persona{ID: "helper"})
	for _, name := range []string{"write_memory", "delete_memory"} {
		if _, ok := blocks[name]; !ok {
			t.Fatalf("proposal-only should block %s: %#v", name, blocks)
		}
	}
	if _, ok := blocks["remember_memory"]; ok {
		t.Fatalf("explicit remember tool should remain available in proposal-only mode: %#v", blocks)
	}

	if blocks := memoryToolBlocks(&persona.Persona{ID: "helper", MemoryPolicy: "auto_commit"}); blocks != nil {
		t.Fatalf("auto_commit should expose memory tools, got %#v", blocks)
	}
}

func TestMergeToolBlocks(t *testing.T) {
	blocks := mergeToolBlocks(
		map[string]string{"task_create": "task"},
		map[string]string{"write_memory": "memory"},
		nil,
	)
	if blocks["task_create"] != "task" || blocks["write_memory"] != "memory" {
		t.Fatalf("merged blocks = %#v", blocks)
	}
}
