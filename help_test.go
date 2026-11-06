package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	for _, command := range [][]string{nil, {"exec"}, {"stop"}, {"status"}, {"server"}, {"service"}, {"service", "install"}, {"service", "start"}, {"service", "restart"}, {"service", "stop"}} {
		for _, flag := range []string{"-h", "--help"} {
			args := append(append([]string{}, command...), flag)
			cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestHelpProcess$", "--"}, args...)...)
			cmd.Env = append(os.Environ(), "TEST_HELP_PROCESS=1")
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.HasPrefix(string(output), "Usage: macos-super-daemon") {
				t.Errorf("help %q: output %q, error %v", args, output, err)
			}
		}
	}
	if _, ok := helpMessage([]string{"exec", "command", "--help"}); ok {
		t.Fatal("intercepted a command argument")
	}
}

func TestHelpProcess(t *testing.T) {
	if os.Getenv("TEST_HELP_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := run(os.Args[i+1:]); err != nil {
				t.Fatal(err)
			}
			os.Exit(0)
		}
	}
	t.Fatal("missing argument separator")
}
