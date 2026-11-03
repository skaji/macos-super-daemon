package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartAndReap(t *testing.T) {
	dir := t.TempDir()
	s := &server{dir: dir, children: make(map[int]*exec.Cmd)}
	p, err := s.start(request{Args: []string{"sh", "-c", "printf '%s:%s' \"$TEST_VALUE\" \"$PWD\"; printf error >&2; exit 7"}, Dir: dir, Env: []string{"PATH=/bin:/usr/bin", "TEST_VALUE=hello"}})
	if err != nil {
		t.Fatal(err)
	}
	s.wg.Wait()
	if p.PID == 0 {
		t.Fatal("missing PID")
	}
	got := s.processes[0]
	if got.Exited == nil || got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("unexpected exit state: %+v", got)
	}
	if len(s.children) != 0 {
		t.Fatal("child was not reaped")
	}
	output, err := os.ReadFile(p.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "hello:"+realDir {
		t.Fatalf("stdout: %q", output)
	}
	output, err = os.ReadFile(p.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "error" {
		t.Fatalf("stderr: %q", output)
	}
}

func TestStartMissingCommand(t *testing.T) {
	dir := t.TempDir()
	s := &server{dir: dir, children: make(map[int]*exec.Cmd)}
	_, err := s.start(request{Args: []string{"missing-command"}, Dir: dir, Env: []string{"PATH=" + dir}})
	if err == nil || !strings.Contains(err.Error(), "command not found") {
		t.Fatalf("unexpected error: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil || len(files) != 0 {
		t.Fatalf("unexpected log files: %v, %v", files, err)
	}
}
