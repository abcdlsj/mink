package memory

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	store *Store
	mu    sync.Mutex
	tm    map[string]*time.Timer
}

func NewWatcher(store *Store) *Watcher {
	return &Watcher{store: store, tm: make(map[string]*time.Timer)}
}

func (w *Watcher) Start(ctx context.Context) error {
	if w == nil || w.store == nil || w.store.db == nil {
		return nil
	}
	if err := os.MkdirAll(w.store.root, 0o755); err != nil {
		return err
	}
	if err := w.store.Sync(ctx); err != nil {
		return err
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := w.addDirs(fw, w.store.root); err != nil {
		_ = fw.Close()
		return err
	}
	go w.loop(ctx, fw)
	return nil
}

func (w *Watcher) loop(ctx context.Context, fw *fsnotify.Watcher) {
	defer fw.Close()
	for {
		select {
		case ev, ok := <-fw.Events:
			if !ok {
				return
			}
			w.handle(ctx, fw, ev)
		case <-fw.Errors:
		case <-ctx.Done():
			return
		}
	}
}

func (w *Watcher) handle(ctx context.Context, fw *fsnotify.Watcher, ev fsnotify.Event) {
	if ev.Name == "" {
		return
	}
	if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
		if ev.Op&fsnotify.Create != 0 {
			_ = w.addDirs(fw, ev.Name)
		}
		return
	}
	if filepath.Ext(ev.Name) != ".md" {
		return
	}
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
		w.schedule(ctx, ev.Name)
	}
}

func (w *Watcher) addDirs(fw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || !d.IsDir() {
			return err
		}
		return fw.Add(path)
	})
}

func (w *Watcher) schedule(ctx context.Context, path string) {
	w.mu.Lock()
	if t := w.tm[path]; t != nil {
		t.Stop()
	}
	w.tm[path] = time.AfterFunc(200*time.Millisecond, func() {
		if _, err := os.Stat(path); err != nil {
			_ = w.store.DeletePath(ctx, path)
		} else {
			_ = w.store.SyncPath(ctx, path)
		}
		w.mu.Lock()
		delete(w.tm, path)
		w.mu.Unlock()
	})
	w.mu.Unlock()
}
