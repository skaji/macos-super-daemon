# macos-super-daemon

Run commands as children of a single per-user macOS LaunchAgent. The server listens on a Unix domain socket in `~/.macos-super-daemon`. Commands inherit the invoking client's working directory and environment, and their stdout and stderr are written to separate files in that directory.

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
```

`exec` starts the command directly, without a shell. Use `exec sh -c '...'` explicitly for shell syntax. The command's stdin is disconnected. `exec` prints the process ID and log paths; `status` shows running and exited children from the current server instance. An exited child is reaped and is not restarted. Status history is lost when the server restarts, but log files remain. When the server stops, it kills and reaps its direct children. Processes that detach or spawn descendants are not tracked individually.
