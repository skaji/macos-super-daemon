# macos-super-daemon

Run commands as children of a single per-user macOS LaunchAgent. The server listens on a Unix domain socket in `~/.macos-super-daemon`. Commands inherit the invoking client's working directory and environment, and their stdout and stderr are combined in `~/.macos-super-daemon/log.txt`. Each line is prefixed with the PID; the log also records starts and exits. On startup, the server logs its start time and its complete environment, including any secrets in environment variables.

## Installation

Build the executable and place it at a stable absolute path, for example:

```sh
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/macos-super-daemon" .
```

Create `~/Library/LaunchAgents/com.github.skaji.macos-super-daemon.plist`, replacing `/Users/YOUR_NAME/.local/bin/macos-super-daemon` with the absolute path to the executable:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.github.skaji.macos-super-daemon</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/YOUR_NAME/.local/bin/macos-super-daemon</string>
    <string>server</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
```

Load it in the current user's graphical session:

```sh
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.github.skaji.macos-super-daemon.plist"
```

To unload it, run `launchctl bootout "gui/$(id -u)/com.github.skaji.macos-super-daemon"`.

## Usage

```sh
macos-super-daemon exec sleep 3600
macos-super-daemon exec /path/to/command arg1 arg2
macos-super-daemon status
macos-super-daemon stop PID
```

`exec` starts the command directly, without a shell. Use `exec sh -c '...'` explicitly for shell syntax. The command's stdin is disconnected. `exec` prints the PID and log path; `status` prints a JSON array of running children. `stop PID` sends SIGTERM to a running child and returns without waiting for it to exit. An exited child is reaped and removed from status; it is not restarted. The log is retained when the server restarts. The operating system can reuse PIDs after processes exit. Output without a trailing newline is logged when the process exits. Output pipes inherited by descendants are closed two seconds after the direct child exits. When the server stops, it sends SIGTERM to its direct children, waits up to two seconds, then sends SIGKILL to any remaining children and reaps them. Processes that detach or spawn descendants are not tracked individually.
