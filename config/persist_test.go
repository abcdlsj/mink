package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistModelPathWritesActiveModelOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	in := `active_model = "main"
default_model = "main"

[api_keys]
OPENAI_API_KEY = "${OPENAI_API_KEY}"

[models.main]
provider = "openai"
model = "gpt-4.1-mini"
api_key = "OPENAI_API_KEY"

[models.big]
provider = "openai"
model = "gpt-5"
api_key = "OPENAI_API_KEY"
`
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PersistModelPath(path, Config{ActiveModel: "big"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, `active_model = "big"`) {
		t.Fatalf("active_model not persisted:\n%s", out)
	}
	if !strings.Contains(out, `api_key = "OPENAI_API_KEY"`) || strings.Contains(out, "sk-test") {
		t.Fatalf("model config not preserved:\n%s", out)
	}
}

func TestPersistModelPathWritesDirectModelWithoutSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	in := `active_model = "main"
default_model = "main"
api_key = "OPENAI_API_KEY"

[api_keys]
OPENAI_API_KEY = "${OPENAI_API_KEY}"
`
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Provider: "openai",
		Model:    "gpt-5",
		APIKey:   "sk-test",
	}
	if err := PersistModelPath(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		`active_model = ""`,
		`default_model = ""`,
		`provider = "openai"`,
		`model = "gpt-5"`,
		`api_key = "OPENAI_API_KEY"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk-test") {
		t.Fatalf("secret leaked:\n%s", out)
	}
}
