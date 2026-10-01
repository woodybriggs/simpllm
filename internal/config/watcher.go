package config

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher watches config files for changes and signals when a reload is needed.
type Watcher struct {
	fs      *fsnotify.Watcher
	paths   []string
	dirs    []string
	changes chan struct{}
	done    chan struct{}
	closeMu sync.Once
}

// NewWatcher creates a watcher for the given config file paths.
func NewWatcher(paths []string) (*Watcher, error) {
	w := &Watcher{
		paths:   paths,
		changes: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w.fs = fsWatcher

	seenDirs := make(map[string]bool)
	for _, p := range paths {
		if p == "" {
			continue
		}
		// Watch the file itself (if it exists).
		fsWatcher.Add(p)

		// Watch parent dir to detect file creation.
		dir := filepath.Dir(p)
		if !seenDirs[dir] {
			seenDirs[dir] = true
			w.dirs = append(w.dirs, dir)
			if err := fsWatcher.Add(dir); err != nil {
				log.Printf("config watcher: cannot watch dir %s: %v", dir, err)
			}
		}
	}

	go w.loop()
	return w, nil
}

// Changes returns a channel that receives a signal when a config file changes.
func (w *Watcher) Changes() <-chan struct{} {
	return w.changes
}

// Close stops watching.
func (w *Watcher) Close() error {
	close(w.done)
	w.closeMu.Do(func() { w.fs.Close() })
	return nil
}

func (w *Watcher) loop() {
	defer close(w.changes)

	var debounce *time.Timer

	for {
		select {
		case <-w.done:
			if debounce != nil {
				debounce.Stop()
			}
			return

		case event, ok := <-w.fs.Events:
			if !ok {
				return
			}
			// Only care about writes, creates, renames (editor save patterns).
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if !w.isRelevant(event.Name) {
				continue
			}

			log.Printf("config watcher: change detected in %s", event.Name)

			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				select {
				case w.changes <- struct{}{}:
				default:
				}
			})

		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			// A bad file descriptor means the underlying watcher is dead.
			// Close fsnotify to stop its internal goroutine from spinning,
			// then park until Close() is called. The goroutine stays alive
			// to maintain Go runtime signal-handling state on macOS
			// (Go 1.27.1 signal_recv inconsistent state bug).
			if isWatcherDead(err) {
				log.Printf("config watcher: %v — stopped", err)
				// Don't close fsnotify here — doing so from the watcher
				// goroutine can invalidate FDs still in use by the main
				// goroutine (kqueue close races with socket creation on
				// macOS). Just park until Close() handles cleanup.
				<-w.done
				return
			}
			log.Printf("config watcher error: %v", err)
		}
	}
}

func (w *Watcher) isRelevant(name string) bool {
	for _, p := range w.paths {
		if name == p {
			return true
		}
	}
	// Check if it's a config.yaml creation in a watched dir.
	for _, dir := range w.dirs {
		if filepath.Dir(name) == dir && filepath.Base(name) == "config.yaml" {
			return true
		}
	}
	return false
}

// isWatcherDead reports whether an fsnotify error indicates the watcher is
// permanently broken (e.g. bad file descriptor under launchd).
func isWatcherDead(err error) bool {
	s := err.Error()
	return strings.Contains(s, "bad file descriptor") || strings.Contains(s, "closed network connection")
}
