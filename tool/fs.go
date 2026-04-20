package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Read struct {
	workspace string
}

func (t *Read) Name() string { return "read" }
func (t *Read) Desc() string { return "Read file contents" }
func (t *Read) Schema() map[string]any {
	return objectSchema(
		prop("path", "string", "File path to read"),
		required("path"),
	)
}

func (t *Read) Run(ctx context.Context, args json.RawMessage) (string, error) {
	in, err := decodeArgs[struct {
		Path string `json:"path"`
	}]("read", args)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(resolvePath(t.workspace, in.Path))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type Write struct {
	workspace string
}

func (t *Write) Name() string { return "write" }
func (t *Write) Desc() string { return "Write file contents" }
func (t *Write) Schema() map[string]any {
	return objectSchema(
		prop("path", "string", "File path to write"),
		prop("content", "string", "File content"),
		required("path", "content"),
	)
}

func (t *Write) Run(ctx context.Context, args json.RawMessage) (string, error) {
	in, err := decodeArgs[struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}]("write", args)
	if err != nil {
		return "", err
	}
	path := resolvePath(t.workspace, in.Path)
	if err := ensureWritePath(t.workspace, path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(in.Content), path), nil
}

type Edit struct {
	workspace string
}

func (t *Edit) Name() string { return "edit" }
func (t *Edit) Desc() string { return "Edit file by search and replace" }
func (t *Edit) Schema() map[string]any {
	return objectSchema(
		prop("path", "string", "File path"),
		prop("old", "string", "Text to replace"),
		prop("new", "string", "Replacement text"),
		required("path", "old", "new"),
	)
}

func (t *Edit) Run(ctx context.Context, args json.RawMessage) (string, error) {
	in, err := decodeArgs[struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}]("edit", args)
	if err != nil {
		return "", err
	}
	path := resolvePath(t.workspace, in.Path)
	if err := ensureWritePath(t.workspace, path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	if !strings.Contains(content, in.Old) {
		return "", fmt.Errorf("search text not found")
	}
	content = strings.Replace(content, in.Old, in.New, 1)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

func decodeArgs[T any](name string, args json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(args, &v); err != nil {
		return v, ParseError(name, err.Error(), string(args))
	}
	return v, nil
}

func resolvePath(workspace, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return workspace
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if workspace == "" {
		abs, err := filepath.Abs(path)
		if err == nil {
			return abs
		}
		return filepath.Clean(path)
	}
	return filepath.Join(workspace, path)
}

func ensureWritePath(workspace, path string) error {
	if workspace == "" {
		return nil
	}
	rel, err := filepath.Rel(workspace, path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("write outside workspace is not allowed: %s", path)
	}
	return nil
}
