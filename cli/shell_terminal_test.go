package cli

import "testing"

func TestCleanTerminalInputStripsMouseReports(t *testing.T) {
	in := "看下 [<65;83;19M\x1b[<65;83;19m[<64;82;18M Sumi"
	got := cleanTerminalInput(in)
	want := "看下  Sumi"
	if got != want {
		t.Fatalf("cleanTerminalInput() = %q, want %q", got, want)
	}
}
