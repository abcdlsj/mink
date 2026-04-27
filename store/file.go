package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Store struct {
	sessionsDir    string
	sessionIndex   string
	current        string
	runlogDir      string
	taskRunlogDir  string
	globalRunlog   string
	legacySessions string
	legacyRunlog   string
	mu             sync.Mutex
}

func Open(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	s := &Store{
		sessionsDir:    filepath.Join(root, "sessions"),
		sessionIndex:   filepath.Join(root, "state", "session_index.json"),
		current:        filepath.Join(root, "state", "current_sessions.json"),
		runlogDir:      filepath.Join(root, "runlog"),
		taskRunlogDir:  filepath.Join(root, "runlog", "tasks"),
		globalRunlog:   filepath.Join(root, "runlog", "global.jsonl"),
		legacySessions: filepath.Join(root, "sessions.jsonl"),
		legacyRunlog:   filepath.Join(root, "runlog.jsonl"),
	}
	if err := s.ensurePaths(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) ensurePaths() error {
	for _, dir := range []string{
		s.sessionsDir,
		filepath.Dir(s.current),
		s.runlogDir,
		s.taskRunlogDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return touch(s.globalRunlog)
}

func (s *Store) Close() error { return nil }

func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func trimID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("id is empty")
	}
	return id, nil
}

func (s *Store) sessionPath(id, source string, when time.Time) string {
	tag, date := pathKey(id, source, when)
	return filepath.Join(s.sessionsDir, tag, date, id+".json")
}

func (s *Store) runlogPath(id, source string, when time.Time) string {
	tag, date := pathKey(id, source, when)
	return filepath.Join(s.runlogDir, tag, date, id+".jsonl")
}

func (s *Store) taskRunlogPath(id string) string {
	return filepath.Join(s.taskRunlogDir, strings.TrimSpace(id)+".jsonl")
}

func pathKey(id, source string, when time.Time) (string, string) {
	tag := sourceBucket(source)
	date := dateBucket(when)
	if d, t, ok := parseSessionID(id); ok {
		date = d
		if tag == "default" {
			tag = t
		}
	}
	return tag, date
}

func sourceBucket(source string) string {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return "default"
	}
	if i := strings.IndexByte(source, ':'); i >= 0 {
		source = source[:i]
	}
	var b strings.Builder
	lastDash := false
	for _, r := range source {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 12 {
			break
		}
	}
	tag := strings.Trim(b.String(), "-")
	if tag == "" {
		return "default"
	}
	return tag
}

func dateBucket(when time.Time) string {
	if when.IsZero() {
		return "unknown"
	}
	return when.Format("20060102")
}

func parseSessionID(id string) (date, tag string, ok bool) {
	id = strings.TrimSpace(id)
	if len(id) < 18 || id[8] != '-' {
		return "", "", false
	}
	date = id[:8]
	i := strings.LastIndexByte(id, '-')
	if i <= 9 || len(id[i+1:]) != 8 {
		return "", "", false
	}
	tag = strings.TrimSpace(id[9:i])
	if tag == "" {
		return "", "", false
	}
	hash := id[i+1:]
	for _, r := range date {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", "", false
		}
	}
	return date, tag, true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func walkFiles(root, ext string, fn func(string) error) error {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		if ext == "" || filepath.Ext(root) == ext {
			return fn(root)
		}
		return nil
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext != "" && filepath.Ext(path) != ext {
			return nil
		}
		return fn(path)
	})
}

func findFile(root, name string) (string, bool, error) {
	var found string
	err := walkFiles(root, filepath.Ext(name), func(path string) error {
		if filepath.Base(path) != name {
			return nil
		}
		found = path
		return fs.SkipAll
	})
	if err != nil && err != fs.SkipAll {
		return "", false, err
	}
	return found, found != "", nil
}
