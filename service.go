package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const serviceLabel = "com.github.skaji.macos-super-daemon"

func runService(home string, args []string) error {
	return service(home, args, os.Executable, launchctl)
}

func service(home string, args []string, executablePath func() (string, error), runLaunchctl func(...string) error) error {
	if len(args) != 1 {
		return errors.New("usage: macos-super-daemon service install|start|restart|stop")
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
	domain := "gui/" + strconv.Itoa(os.Getuid())
	switch args[0] {
	case "install":
		executable, err := executablePath()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		return installService(plist, executable)
	case "start":
		return runLaunchctl("bootstrap", domain, plist)
	case "restart":
		return runLaunchctl("kill", "SIGTERM", domain+"/"+serviceLabel)
	case "stop":
		return runLaunchctl("bootout", domain+"/"+serviceLabel)
	default:
		return fmt.Errorf("unknown service subcommand: %s", args[0])
	}
}

func installService(path, executable string) error {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(executable)); err != nil {
		return err
	}
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>server</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
`, serviceLabel, escaped.String())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create plist: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write plist: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close plist: %w", err)
	}
	fmt.Printf("Installed %s\n", path)
	return nil
}

func launchctl(args ...string) error {
	cmd := exec.Command("/bin/launchctl", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launchctl %s: %w", args[0], err)
	}
	return nil
}
