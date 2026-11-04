package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var version = "dev"

type request struct {
	Action string   `json:"action"`
	PID    int      `json:"pid,omitempty"`
	Args   []string `json:"args,omitempty"`
	Dir    string   `json:"dir,omitempty"`
	Env    []string `json:"env,omitempty"`
}

type process struct {
	PID     int       `json:"pid"`
	Args    []string  `json:"args"`
	Dir     string    `json:"dir"`
	Started time.Time `json:"started"`
}

type child struct {
	process process
	cmd     *exec.Cmd
}

type response struct {
	Error     string    `json:"error,omitempty"`
	Process   *process  `json:"process,omitempty"`
	Processes []process `json:"processes,omitempty"`
}

type server struct {
	log      *logFile
	mu       sync.Mutex
	children map[int]*child
	wg       sync.WaitGroup
}

type logFile struct {
	mu   sync.Mutex
	file *os.File
}

func (l *logFile) line(pid int, message []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lineLocked(pid, message)
}

func (l *logFile) lineLocked(pid int, message []byte) error {
	_, err := fmt.Fprintf(l.file, "%s %d %s\n", time.Now().Format(time.RFC3339), pid, message)
	return err
}

type processOutput struct {
	log     *logFile
	pid     int
	ready   chan struct{}
	pending []byte
}

func (w *processOutput) Write(p []byte) (int, error) {
	<-w.ready
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		if err := w.log.line(w.pid, w.pending[:i]); err != nil {
			return 0, err
		}
		w.pending = w.pending[i+1:]
	}
	return len(p), nil
}

func (w *processOutput) flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	err := w.log.line(w.pid, w.pending)
	w.pending = nil
	return err
}

func (s *server) start(req request) (*process, error) {
	if len(req.Args) == 0 || req.Args[0] == "" {
		return nil, errors.New("command is required")
	}
	if req.Dir == "" {
		return nil, errors.New("working directory is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	output := &processOutput{log: s.log, ready: make(chan struct{})}
	cmd := exec.Command(req.Args[0], req.Args[1:]...)
	// Herdr uses HC_SCOPE to terminate processes when a terminal closes.
	env := make([]string, 0, len(req.Env))
	for _, variable := range req.Env {
		if !strings.HasPrefix(variable, "HC_SCOPE=") {
			env = append(env, variable)
		}
	}
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = req.Dir, env, output, output
	cmd.WaitDelay = 2 * time.Second
	// Look up the executable using the client's PATH rather than launchd's PATH.
	if !filepath.IsAbs(req.Args[0]) && filepath.Dir(req.Args[0]) == "." {
		path := ""
		for _, e := range req.Env {
			if len(e) >= 5 && e[:5] == "PATH=" {
				path = e[5:]
				break
			}
		}
		found := false
		for _, d := range filepath.SplitList(path) {
			if d == "" {
				d = req.Dir
			} else if !filepath.IsAbs(d) {
				d = filepath.Join(req.Dir, d)
			}
			candidate := filepath.Join(d, req.Args[0])
			if info, e := os.Stat(candidate); e == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				cmd.Path = candidate
				cmd.Err = nil
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("command not found in PATH: %s", req.Args[0])
		}
	}
	// Hold the log lock until the start record is written so output cannot precede it.
	s.log.mu.Lock()
	if err := cmd.Start(); err != nil {
		s.log.mu.Unlock()
		return nil, err
	}
	output.pid = cmd.Process.Pid
	if err := s.log.lineLocked(output.pid, fmt.Appendf(nil, "start command=%q", req.Args)); err != nil {
		fmt.Fprintln(os.Stderr, "write log:", err)
	}
	close(output.ready)
	s.log.mu.Unlock()
	p := &process{PID: cmd.Process.Pid, Args: req.Args, Dir: req.Dir, Started: time.Now()}
	s.children[p.PID] = &child{process: *p, cmd: cmd}
	s.wg.Go(func() {
		_ = cmd.Wait()
		if flushErr := output.flush(); flushErr != nil {
			fmt.Fprintln(os.Stderr, "write log:", flushErr)
		}
		message := fmt.Appendf(nil, "exit code=%d", cmd.ProcessState.ExitCode())
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			sig := status.Signal()
			message = fmt.Appendf(nil, "exit signal=%d (%s)", sig, sig)
		}
		if logErr := s.log.line(p.PID, message); logErr != nil {
			fmt.Fprintln(os.Stderr, "write log:", logErr)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if current := s.children[p.PID]; current != nil && current.cmd == cmd {
			delete(s.children, p.PID)
		}
	})
	result := *p
	return &result, nil
}

func (s *server) stop(pid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	child, ok := s.children[pid]
	if !ok {
		return fmt.Errorf("no running child with PID %d", pid)
	}
	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to PID %d: %w", pid, err)
	}
	return nil
}

func (s *server) shutdown(timeout time.Duration) {
	s.mu.Lock()
	for _, child := range s.children {
		_ = child.cmd.Process.Signal(syscall.SIGTERM)
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}

	s.mu.Lock()
	for _, child := range s.children {
		_ = child.cmd.Process.Kill()
	}
	s.mu.Unlock()
	<-done
}

func (s *server) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var req request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(response{Error: err.Error()})
		return
	}
	var res response
	switch req.Action {
	case "exec":
		p, err := s.start(req)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Process = p
		}
	case "stop":
		if err := s.log.line(req.PID, []byte("stop requested signal=SIGTERM")); err != nil {
			fmt.Fprintln(os.Stderr, "write log:", err)
		}
		if err := s.stop(req.PID); err != nil {
			res.Error = err.Error()
			if logErr := s.log.line(req.PID, fmt.Appendf(nil, "stop failed: %s", err)); logErr != nil {
				fmt.Fprintln(os.Stderr, "write log:", logErr)
			}
		}
	case "status":
		s.mu.Lock()
		res.Processes = make([]process, 0, len(s.children))
		for _, child := range s.children {
			res.Processes = append(res.Processes, child.process)
		}
		s.mu.Unlock()
	default:
		res.Error = "unknown action"
	}
	_ = json.NewEncoder(conn).Encode(res)
}

func runServer(dir string) error {
	started := time.Now()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another server may be running: %w", err)
	}
	socket := filepath.Join(dir, "server.sock")
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(dir, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	s := &server{log: &logFile{file: log}, children: make(map[int]*child)}
	if err := s.log.line(os.Getpid(), fmt.Appendf(nil, "server start time=%s", started.Format(time.RFC3339))); err != nil {
		return fmt.Errorf("write server start to log: %w", err)
	}
	for _, variable := range os.Environ() {
		if err := s.log.line(os.Getpid(), fmt.Appendf(nil, "server env %q", variable)); err != nil {
			return fmt.Errorf("write server environment to log: %w", err)
		}
	}
	var handlers sync.WaitGroup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case sig := <-stop:
			name := "SIGTERM"
			if sig == os.Interrupt {
				name = "SIGINT"
			}
			if err := s.log.line(os.Getpid(), fmt.Appendf(nil, "server stopping signal=%s", name)); err != nil {
				fmt.Fprintln(os.Stderr, "write log:", err)
			}
			_ = listener.Close()
		case <-done:
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			return err
		}
		handlers.Go(func() { s.handle(conn) })
	}
	handlers.Wait()
	s.shutdown(2 * time.Second)
	if err := s.log.line(os.Getpid(), []byte("server stopped")); err != nil {
		return fmt.Errorf("write server stop to log: %w", err)
	}
	return nil
}

func call(dir string, req request) (response, error) {
	conn, err := net.Dial("unix", filepath.Join(dir, "server.sock"))
	if err != nil {
		return response{}, fmt.Errorf("connect to server: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return response{}, err
	}
	var res response
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		return res, err
	}
	if res.Error != "" {
		return res, errors.New(res.Error)
	}
	return res, nil
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: macos-super-daemon --version | server | exec command [args...] | stop PID | status")
	}
	if args[0] == "--version" {
		if len(args) != 1 {
			return errors.New("usage: macos-super-daemon --version")
		}
		_, err := fmt.Fprintln(os.Stdout, version)
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".macos-super-daemon")
	switch args[0] {
	case "server":
		if len(args) != 1 {
			return errors.New("usage: macos-super-daemon server")
		}
		return runServer(dir)
	case "exec":
		if len(args) < 2 {
			return errors.New("usage: macos-super-daemon exec command [args...]")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		res, err := call(dir, request{Action: "exec", Args: args[1:], Dir: cwd, Env: os.Environ()})
		if err != nil {
			return err
		}
		fmt.Printf("Started PID %d\nlog: %s\n", res.Process.PID, filepath.Join(dir, "log.txt"))
		return nil
	case "stop":
		if len(args) != 2 {
			return errors.New("usage: macos-super-daemon stop PID")
		}
		pid, err := strconv.Atoi(args[1])
		if err != nil || pid <= 0 {
			return fmt.Errorf("invalid PID: %s", args[1])
		}
		if _, err := call(dir, request{Action: "stop", PID: pid}); err != nil {
			return err
		}
		fmt.Printf("Sent SIGTERM to PID %d\n", pid)
		return nil
	case "status":
		if len(args) != 1 {
			return errors.New("usage: macos-super-daemon status")
		}
		res, err := call(dir, request{Action: "status"})
		if err != nil {
			return err
		}
		if res.Processes == nil {
			res.Processes = []process{}
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(res.Processes)
	default:
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
