package runtime

type Driver struct {
	Name        string
	Command     string
	StdinPrompt bool
	BuildArgs   func(prompt, mcpConfigPath, workDir, sessionID string) []string
	ParseOutput func(line string) *Message
}
