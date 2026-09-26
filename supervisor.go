package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const maxLogLine = 1024 * 1024

type processEvent struct {
	index    int
	runID    uint64
	kind     string
	line     string
	exitCode int
	err      string
}

type managedProcess struct {
	cmd      *exec.Cmd
	done     chan struct{}
	runID    uint64
	stopping bool
}

type supervisor struct {
	services []Service
	events   chan processEvent
	mu       sync.Mutex
	process  map[int]*managedProcess
	watchers []*fsnotify.Watcher
	nextRun  uint64
}

func newSupervisor(services []Service) *supervisor {
	return &supervisor{
		services: services,
		events:   make(chan processEvent, 512),
		process:  make(map[int]*managedProcess),
	}
}

func (s *supervisor) start(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.process[index]; running {
		return
	}
	s.nextRun++
	runID := s.nextRun
	service := s.services[index]
	cmd := exec.Command("sh", "-c", service.Command)
	cmd.Dir = service.Cwd
	cmd.Env = serviceEnvironment(service.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.emit(processEvent{index: index, runID: runID, kind: "failed", exitCode: -1, err: err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		s.emit(processEvent{index: index, runID: runID, kind: "failed", exitCode: -1, err: err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		s.emit(processEvent{index: index, runID: runID, kind: "failed", exitCode: -1, err: err.Error()})
		return
	}
	proc := &managedProcess{cmd: cmd, done: make(chan struct{}), runID: runID}
	s.process[index] = proc
	s.emit(processEvent{index: index, runID: proc.runID, kind: "started"})

	var readers sync.WaitGroup
	readers.Add(2)
	go s.readOutput(&readers, stdout, index, proc.runID)
	go s.readOutput(&readers, stderr, index, proc.runID)
	go func() {
		waitErr := cmd.Wait()
		readers.Wait()
		code := 0
		errText := ""
		if waitErr != nil {
			errText = waitErr.Error()
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			} else {
				code = -1
			}
		}
		s.mu.Lock()
		stopping := proc.stopping
		if s.process[index] == proc {
			delete(s.process, index)
		}
		s.mu.Unlock()
		kind := "exited"
		if stopping {
			kind = "stopped"
		} else if code != 0 {
			kind = "failed"
		}
		s.emit(processEvent{index: index, runID: proc.runID, kind: kind, exitCode: code, err: errText})
		close(proc.done)
	}()
}

func serviceEnvironment(extra map[string]string) []string {
	env := make(map[string]string)
	for _, value := range os.Environ() {
		key, val, ok := strings.Cut(value, "=")
		if ok {
			env[key] = val
		}
	}
	for key, value := range extra {
		env[key] = value
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func (s *supervisor) readOutput(readers *sync.WaitGroup, output interface{ Read([]byte) (int, error) }, index int, runID uint64) {
	defer readers.Done()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 64*1024), maxLogLine)
	for scanner.Scan() {
		s.emit(processEvent{index: index, runID: runID, kind: "line", line: scanner.Text()})
	}
	if err := scanner.Err(); err != nil {
		s.emit(processEvent{index: index, runID: runID, kind: "line", line: fmt.Sprintf("[output read error: %v]", err)})
	}
}

func (s *supervisor) emit(event processEvent) {
	s.events <- event
}

func (s *supervisor) stop(index int) {
	s.mu.Lock()
	proc := s.process[index]
	if proc != nil {
		proc.stopping = true
	}
	s.mu.Unlock()
	if proc == nil {
		return
	}
	_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-proc.done:
		return
	case <-time.After(1500 * time.Millisecond):
		_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGKILL)
		<-proc.done
	}
}

func (s *supervisor) stopAll() {
	var wait sync.WaitGroup
	for i := range s.services {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			s.stop(index)
		}(i)
	}
	wait.Wait()
	s.mu.Lock()
	watchers := s.watchers
	s.watchers = nil
	s.mu.Unlock()
	for _, watcher := range watchers {
		_ = watcher.Close()
	}
}

func (s *supervisor) restart(index int) {
	s.stop(index)
	s.start(index)
}
