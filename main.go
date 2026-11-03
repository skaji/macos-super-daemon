package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type request struct {
	Action string   `json:"action"`
	Args   []string `json:"args,omitempty"`
	Dir    string   `json:"dir,omitempty"`
	Env    []string `json:"env,omitempty"`
}

type process struct {
	ID       int        `json:"id"`
	PID      int        `json:"pid"`
	Args     []string   `json:"args"`
	Dir      string     `json:"dir"`
	Started  time.Time  `json:"started"`
	Exited   *time.Time `json:"exited,omitempty"`
	ExitCode *int       `json:"exit_code,omitempty"`
	Error    string     `json:"error,omitempty"`
	Stdout   string     `json:"stdout"`
	Stderr   string     `json:"stderr"`
}

type response struct {
	Error     string    `json:"error,omitempty"`
	Process   *process  `json:"process,omitempty"`
	Processes []process `json:"processes,omitempty"`
}

type server struct {
	dir       string
	mu        sync.Mutex
	processes []*process
	children  map[int]*exec.Cmd
	wg        sync.WaitGroup
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
	id := len(s.processes) + 1
	// Include a timestamp because IDs restart when the server restarts.
	prefix := fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405.000000000"), id)
	stdout := filepath.Join(s.dir, prefix+".stdout.log")
	stderr := filepath.Join(s.dir, prefix+".stderr.log")
	out, err := os.OpenFile(stdout, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	errout, err := os.OpenFile(stderr, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = out.Close()
		_ = os.Remove(stdout)
		return nil, err
	}
	cmd := exec.Command(req.Args[0], req.Args[1:]...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = req.Dir, req.Env, out, errout
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
			_ = out.Close()
			_ = errout.Close()
			_ = os.Remove(stdout)
			_ = os.Remove(stderr)
			return nil, fmt.Errorf("command not found in PATH: %s", req.Args[0])
		}
	}
	if err := cmd.Start(); err != nil {
		_ = out.Close()
		_ = errout.Close()
		_ = os.Remove(stdout)
		_ = os.Remove(stderr)
		return nil, err
	}
	_ = out.Close()
	_ = errout.Close()
	p := &process{ID: id, PID: cmd.Process.Pid, Args: req.Args, Dir: req.Dir, Started: time.Now(), Stdout: stdout, Stderr: stderr}
	s.processes = append(s.processes, p)
	s.children[p.PID] = cmd
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := cmd.Wait()
		now, code := time.Now(), cmd.ProcessState.ExitCode()
		s.mu.Lock()
		defer s.mu.Unlock()
		p.Exited, p.ExitCode = &now, &code
		if err != nil {
			p.Error = err.Error()
		}
		delete(s.children, p.PID)
	}()
	copy := *p
	return &copy, nil
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
	case "status":
		s.mu.Lock()
		res.Processes = make([]process, len(s.processes))
		for i, p := range s.processes {
			res.Processes[i] = *p
		}
		s.mu.Unlock()
	default:
		res.Error = "unknown action"
	}
	_ = json.NewEncoder(conn).Encode(res)
}

func runServer(dir string) error {
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
	s := &server{dir: dir, children: make(map[int]*exec.Cmd)}
	var handlers sync.WaitGroup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
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
		handlers.Add(1)
		go func() { defer handlers.Done(); s.handle(conn) }()
	}
	handlers.Wait()
	// Terminate direct children when the server exits, then reap them.
	s.mu.Lock()
	for _, cmd := range s.children {
		_ = cmd.Process.Kill()
	}
	s.mu.Unlock()
	s.wg.Wait()
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
		return errors.New("usage: macos-super-daemon server | exec command [args...] | status")
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
		fmt.Printf("Started %d (PID %d)\nstdout: %s\nstderr: %s\n", res.Process.ID, res.Process.PID, res.Process.Stdout, res.Process.Stderr)
		return nil
	case "status":
		if len(args) != 1 {
			return errors.New("usage: macos-super-daemon status")
		}
		res, err := call(dir, request{Action: "status"})
		if err != nil {
			return err
		}
		for _, p := range res.Processes {
			state := "running"
			if p.Exited != nil {
				state = "exited (" + strconv.Itoa(*p.ExitCode) + ")"
			}
			fmt.Printf("%d\t%d\t%s\t%q\n  cwd: %s\n  stdout: %s\n  stderr: %s\n", p.ID, p.PID, state, p.Args, p.Dir, p.Stdout, p.Stderr)
		}
		return nil
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
