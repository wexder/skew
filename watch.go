package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

type watchRoot struct {
	path      string
	directory bool
}

func (s *supervisor) startWatching() {
	for index, service := range s.services {
		if len(service.Watch.Paths) == 0 {
			continue
		}
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			s.emit(processEvent{index: index, kind: "watch-error", err: err.Error()})
			continue
		}
		roots := make([]watchRoot, 0, len(service.Watch.Paths))
		for _, path := range service.Watch.Paths {
			info, err := os.Stat(path)
			if err != nil {
				s.emit(processEvent{index: index, kind: "watch-error", err: fmt.Sprintf("watch %s: %v", path, err)})
				continue
			}
			root := watchRoot{path: path, directory: info.IsDir()}
			roots = append(roots, root)
			if root.directory {
				err = addWatchTree(watcher, path, service)
			} else {
				err = watcher.Add(filepath.Dir(path))
			}
			if err != nil {
				s.emit(processEvent{index: index, kind: "watch-error", err: fmt.Sprintf("watch %s: %v", path, err)})
			}
		}
		if len(roots) == 0 {
			_ = watcher.Close()
			continue
		}
		s.mu.Lock()
		s.watchers = append(s.watchers, watcher)
		s.mu.Unlock()
		go s.watchService(index, service, watcher, roots)
	}
}

func addWatchTree(watcher *fsnotify.Watcher, root string, service Service) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root && service.watchIgnored(path) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

func (s *supervisor) watchService(index int, service Service, watcher *fsnotify.Watcher, roots []watchRoot) {
	var timer *time.Timer
	var timerC <-chan time.Time
	delay := service.Watch.Delay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case event, open := <-watcher.Events:
			if !open {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			path, err := filepath.Abs(event.Name)
			if err != nil || !service.watchMatches(path, roots) || service.watchIgnored(path) {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					if service.watchMatchesDirectory(path, roots) {
						if err := addWatchTree(watcher, path, service); err != nil {
							s.emit(processEvent{index: index, kind: "watch-error", err: fmt.Sprintf("watch %s: %v", path, err)})
						}
					}
				}
			}
			if timer == nil {
				timer = time.NewTimer(delay)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(delay)
			}
			timerC = timer.C
		case err, open := <-watcher.Errors:
			if !open {
				return
			}
			s.emit(processEvent{index: index, kind: "watch-error", err: err.Error()})
		case <-timerC:
			timerC = nil
			s.emit(processEvent{index: index, kind: "watch", line: "files changed"})
		}
	}
}

func (service Service) watchMatches(path string, roots []watchRoot) bool {
	for _, root := range roots {
		if root.directory && pathWithin(root.path, path) || !root.directory && root.path == path {
			return true
		}
	}
	return false
}

func (service Service) watchMatchesDirectory(path string, roots []watchRoot) bool {
	for _, root := range roots {
		if root.directory && pathWithin(root.path, path) {
			return true
		}
	}
	return false
}

func (service Service) watchIgnored(path string) bool {
	relative, err := filepath.Rel(service.Cwd, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	relative = filepath.ToSlash(relative)
	for _, pattern := range service.Watch.Ignore {
		pattern = strings.Trim(strings.TrimSpace(filepath.ToSlash(pattern)), "/")
		if pattern == "" {
			continue
		}
		if globMatch(pattern, relative) || strings.HasPrefix(relative, pattern+"/") {
			return true
		}
	}
	return false
}

func globMatch(pattern, value string) bool {
	patterns, values := strings.Split(pattern, "/"), strings.Split(value, "/")
	var match func(int, int) bool
	match = func(pi, vi int) bool {
		if pi == len(patterns) {
			return vi == len(values)
		}
		if patterns[pi] == "**" {
			if match(pi+1, vi) {
				return true
			}
			return vi < len(values) && match(pi, vi+1)
		}
		if vi == len(values) {
			return false
		}
		ok, err := filepath.Match(patterns[pi], values[vi])
		return err == nil && ok && match(pi+1, vi+1)
	}
	return match(0, 0)
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
