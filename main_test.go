package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testServer(t *testing.T) (*server, string) {
	t.Helper()
	dir := t.TempDir()
	file, err := os.OpenFile(filepath.Join(dir, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return &server{log: &logFile{file: file}, children: make(map[int]*child)}, dir
}

func TestStartAndReap(t *testing.T) {
	s, dir := testServer(t)
	p, err := s.start(request{Args: []string{"sh", "-c", "printf '%s:%s\\n' \"$TEST_VALUE\" \"$PWD\"; printf 'error\\n' >&2; printf partial; exit 7"}, Dir: dir, Env: []string{"PATH=/bin:/usr/bin", "TEST_VALUE=hello"}})
	if err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	if p.PID == 0 {
		t.Fatal("missing PID")
	}
	if len(s.children) != 0 {
		t.Fatal("child was not reaped")
	}
	output, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	for i, line := range lines {
		timestamp, rest, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("missing timestamp: %q", line)
		}
		parsed, err := time.Parse(time.RFC3339, timestamp)
		if err != nil || parsed.Format(time.RFC3339) != timestamp {
			t.Fatalf("invalid timestamp %q: %v", timestamp, err)
		}
		lines[i] = rest
	}
	prefix := fmt.Sprintf("%d ", p.PID)
	if len(lines) != 5 || !strings.HasPrefix(lines[0], prefix+"start command=") || lines[1] != prefix+"hello:"+realDir || lines[2] != prefix+"error" || lines[3] != prefix+"partial" || lines[4] != prefix+"exit code=7" {
		t.Fatalf("unexpected log: %q", output)
	}
}

func TestConcurrentOutput(t *testing.T) {
	s, dir := testServer(t)
	var pids []int
	for i := 1; i <= 4; i++ {
		p, err := s.start(request{Args: []string{"sh", "-c", "for i in 1 2 3 4 5; do echo stdout; echo stderr >&2; done"}, Dir: dir, Env: []string{"PATH=/bin:/usr/bin"}})
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, p.PID)
	}
	s.wg.Wait()
	output, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range pids {
		for _, message := range []string{"stdout", "stderr"} {
			if n := strings.Count(string(output), fmt.Sprintf("%d %s\n", pid, message)); n != 5 {
				t.Fatalf("pid %d, %s: got %d lines; log: %q", pid, message, n, output)
			}
		}
	}
}

func TestStartMissingCommand(t *testing.T) {
	s, dir := testServer(t)
	_, err := s.start(request{Args: []string{"missing-command"}, Dir: dir, Env: []string{"PATH=" + dir}})
	if err == nil || !strings.Contains(err.Error(), "command not found") {
		t.Fatalf("unexpected error: %v", err)
	}
	output, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil || len(output) != 0 {
		t.Fatalf("unexpected log: %q, %v", output, err)
	}
}

func TestStop(t *testing.T) {
	s, dir := testServer(t)
	p, err := s.start(request{Args: []string{"/bin/sleep", "60"}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	client, serverConn := net.Pipe()
	defer func() { _ = client.Close() }()
	go s.handle(serverConn)
	if err := json.NewEncoder(client).Encode(request{Action: "stop", PID: p.PID}); err != nil {
		t.Fatal(err)
	}
	var res response
	if err := json.NewDecoder(client).Decode(&res); err != nil || res.Error != "" {
		t.Fatalf("stop response: %+v, %v", res, err)
	}
	s.wg.Wait()
	output, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	requestLine := fmt.Sprintf("%d stop requested signal=SIGTERM\n", p.PID)
	exitLine := fmt.Sprintf("%d exit signal=%d (%s)\n", p.PID, syscall.SIGTERM, syscall.SIGTERM)
	if start, end := strings.Index(string(output), requestLine), strings.Index(string(output), exitLine); start < 0 || end < start {
		t.Fatalf("unexpected log: %q", output)
	}
	if len(s.children) != 0 {
		t.Fatal("child was not removed")
	}
	if err := s.stop(p.PID); err == nil {
		t.Fatal("stopping an exited child succeeded")
	}
	if err := s.stop(os.Getpid()); err == nil {
		t.Fatal("stopping an unmanaged process succeeded")
	}
}

func TestStopArgumentValidation(t *testing.T) {
	for _, args := range [][]string{{"stop"}, {"stop", "0"}, {"stop", "-1"}, {"stop", "abc"}, {"stop", "1", "2"}} {
		if err := run(args); err == nil {
			t.Errorf("run(%q) succeeded", args)
		}
	}
}

func TestShutdown(t *testing.T) {
	s, dir := testServer(t)
	var children []*child
	for _, script := range []string{
		"trap 'exit 42' TERM; echo ready; while :; do :; done",
		"trap '' TERM; echo ready; while :; do :; done",
	} {
		p, err := s.start(request{Args: []string{"/bin/sh", "-c", script}, Dir: dir, Env: os.Environ()})
		if err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		children = append(children, s.children[p.PID])
		s.mu.Unlock()
	}
	t.Cleanup(func() {
		for _, child := range children {
			_ = child.cmd.Process.Kill()
		}
		s.wg.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		output, err := os.ReadFile(filepath.Join(dir, "log.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(output), " ready\n") == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("children did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.shutdown(250 * time.Millisecond)
	graceful := children[0].cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !graceful.Exited() || graceful.ExitStatus() != 42 {
		t.Fatalf("graceful child: %v", graceful)
	}
	forced := children[1].cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !forced.Signaled() || forced.Signal() != syscall.SIGKILL {
		t.Fatalf("forced child: %v", forced)
	}
	if len(s.children) != 0 {
		t.Fatal("children were not removed")
	}
}

func TestExternalSignal(t *testing.T) {
	s, dir := testServer(t)
	p, err := s.start(request{Args: []string{"/bin/sleep", "60"}, Dir: dir, Env: os.Environ()})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	cmd := s.children[p.PID].cmd
	s.mu.Unlock()
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	output, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d exit signal=%d (%s)\n", p.PID, syscall.SIGKILL, syscall.SIGKILL)
	if !strings.Contains(string(output), want) || strings.Contains(string(output), "stop requested") {
		t.Fatalf("unexpected log: %q", output)
	}
}
