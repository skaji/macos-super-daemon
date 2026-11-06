package main

import "strings"

var helpMessages = map[string]string{
	"": `Usage: macos-super-daemon <command> [arguments]

Run background commands as children of a single LaunchAgent.

Commands:
  exec command [args...]  Start a command
  stop PID                Send SIGTERM to a running command
  status                  List running commands as JSON
  service                 Manage the LaunchAgent
  server                  Run the server

Options:
  -h, --help              Show help
  --version               Show version

Use "macos-super-daemon <command> --help" for command help.
`,
	"exec": `Usage: macos-super-daemon exec command [args...]

Start a command as a server child and print its PID and log path.
Arguments after command are passed to the command unchanged.
`,
	"stop": `Usage: macos-super-daemon stop PID

Send SIGTERM to a running command. This does not wait for it to exit.
`,
	"status": `Usage: macos-super-daemon status

List running commands as JSON.
`,
	"server": `Usage: macos-super-daemon server

Run the server in the foreground.
`,
	"service": `Usage: macos-super-daemon service <command>

Manage the LaunchAgent in the current graphical session.

Commands:
  install  Create the LaunchAgent plist
  start    Load the LaunchAgent
  restart  Restart the server
  stop     Unload the LaunchAgent

Use "macos-super-daemon service <command> --help" for command help.
`,
	"service install": `Usage: macos-super-daemon service install

Create the LaunchAgent plist using the current executable's path.
An existing plist is not overwritten. Run service start to load it.
`,
	"service start": `Usage: macos-super-daemon service start

Load the LaunchAgent into the current graphical session.
`,
	"service restart": `Usage: macos-super-daemon service restart

Send SIGTERM to the server; launchd restarts it. Running commands are
terminated and are not restarted automatically.
`,
	"service stop": `Usage: macos-super-daemon service stop

Unload the LaunchAgent and terminate its running commands.
`,
}

func helpMessage(args []string) (string, bool) {
	if len(args) == 0 || (args[len(args)-1] != "-h" && args[len(args)-1] != "--help") {
		return "", false
	}
	key := strings.Join(args[:len(args)-1], " ")
	message, ok := helpMessages[key]
	return message, ok
}
