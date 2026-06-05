package tool

import "testing"

func TestProposalForFileActions(t *testing.T) {
	write := ProposalFor(Call{Tool: "write", Action: "write /tmp/a.txt"})
	if write.Intent != "Write file" || write.Target != "/tmp/a.txt" || write.Risk != "filesystem_write" {
		t.Fatalf("write proposal = %+v", write)
	}
	read := ProposalFor(Call{Tool: "read", Action: "read /tmp/.env"})
	if read.Intent != "Read sensitive file" || read.Target != "/tmp/.env" || read.Risk != "sensitive_read" {
		t.Fatalf("read proposal = %+v", read)
	}
}
