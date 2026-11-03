# macos-super-daemon

Run background commands on macOS without creating a separate LaunchAgent for each one.
macos-super-daemon runs as a single LaunchAgent and starts your commands as its child processes.
You can check running commands, stop them by PID, and read their combined output in one log file.

## Installation

Download the latest release for Apple silicon Macs:

```sh
mkdir -p "$HOME/.local/bin"
curl -fL https://github.com/skaji/macos-super-daemon/releases/latest/download/macos-super-daemon-darwin-arm64.tar.gz \
  | tar -xz -C "$HOME/.local/bin" macos-super-daemon
```

Make sure `~/.local/bin` is in your `PATH` to run `macos-super-daemon` from the command line.

Create `~/Library/LaunchAgents/com.github.skaji.macos-super-daemon.plist`,
replacing `/Users/YOUR_NAME/.local/bin/macos-super-daemon` with the absolute path to the executable:

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

Load the LaunchAgent in your graphical session:

```sh
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.github.skaji.macos-super-daemon.plist"
```

## Usage

Start a command and check its PID and log path:

```sh
macos-super-daemon exec sleep 3600
macos-super-daemon exec /path/to/command arg1 arg2
```

Commands inherit the current working directory and environment.
They run directly without a shell, and their stdin is disconnected.
To use shell syntax, run a shell explicitly: `macos-super-daemon exec sh -c '...'`.

List running commands as JSON, or stop one by sending it SIGTERM:

```sh
macos-super-daemon status
macos-super-daemon stop PID
```

`stop` returns after sending the signal; it does not wait for the process to exit.
When a command exits, it is removed from `status` and is not restarted.

To check the installed version, run `macos-super-daemon --version`.

## Logs

The server appends its log and all commands' stdout and stderr to `~/.macos-super-daemon/log.txt`.
Each line begins with a timestamp and PID.
The log includes command starts and exits, as well as the server's start time, environment, and shutdown.
Environment variables can contain secrets, which will also be recorded in the log.

Output without a trailing newline is logged when the command exits.
If a command's descendants inherit its output pipes, the pipes are closed two seconds after the command exits.

## Restarting and unloading

To restart the server, for example after updating the binary, send it SIGTERM:

```sh
launchctl kill SIGTERM "gui/$(id -u)/com.github.skaji.macos-super-daemon"
```

The server sends SIGTERM to its running child processes, waits up to two seconds,
and then sends SIGKILL to any that remain. launchd restarts the server automatically,
but you need to start your commands again yourself.

To unload the LaunchAgent:

```sh
launchctl bootout "gui/$(id -u)/com.github.skaji.macos-super-daemon"
```

If you change the plist, unload the agent and load it again with the `bootstrap` command above.
