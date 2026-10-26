package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Spec is one proxy as the server runs it.
type Spec struct {
	// Exec is the absolute path of the file to run. It is never looked up.
	Exec string `json:"exec"`
	// Path is the PATH the program runs with. Empty means the server's own.
	Path string `json:"path,omitempty"`
	// AgentCommand is what the agent types to reach this proxy.
	AgentCommand string   `json:"agent_command"`
	Description  string   `json:"description,omitempty"`
	Deny         []string `json:"deny,omitempty"`
	// InWorkspace runs the command in the directory the caller was in, when
	// that directory is inside one of the caller's workspace roots. Without
	// it the command runs in a private empty directory.
	InWorkspace bool `json:"in_workspace,omitempty"`
	// Env holds the stored secrets and settings the command runs with. They
	// are never sent to a caller.
	Env map[string]string `json:"env,omitempty"`
	// Minted holds the short-lived credentials from env_from. They win over
	// Env. A reload that leaves Minted nil keeps the values the server already
	// holds; an empty, non-nil map clears them. So a change to a stored
	// variable does not need the credentials to be minted again.
	Minted map[string]string `json:"minted"`
}

// Config is what the server was started with.
type Config struct {
	Proxies map[string]Spec
	// Users maps a caller's uid to the proxies it may use.
	Users map[int][]string
	// Roots maps a caller's uid to the directories its workspace calls may
	// run in.
	Roots map[int][]string
}

// RunRequest is a call to a proxy.
type RunRequest struct {
	Args  []string `json:"args"`
	Stdin []byte   `json:"stdin,omitempty"`
	// Cwd is the caller's directory; it is only used by a proxy with InWorkspace.
	Cwd string `json:"cwd,omitempty"`
}

// RunResponse is the outcome of a call. Error is set when the call did not run.
type RunResponse struct {
	Stdout    []byte `json:"stdout,omitempty"`
	Stderr    []byte `json:"stderr,omitempty"`
	Exit      int    `json:"exit"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Listing is a proxy a caller may use. It holds no secrets. The shim matches
// what the agent typed against AgentCommand to find the proxy to call.
type Listing struct {
	Name         string `json:"name"`
	AgentCommand string `json:"agent_command"`
	Description  string `json:"description,omitempty"`
}

// Server answers proxy calls.
type Server struct {
	Config Config
	// Home is the directory under which each call gets a private working directory.
	Home string
	// Path is the PATH the commands run with.
	Path string
	// Timeout ends a call that runs too long; MaxOutput caps stdout and stderr each.
	Timeout   time.Duration
	MaxOutput int
	// Peer returns the uid on the other end of a connection.
	Peer func(net.Conn) (int, error)
	// Admin is the uid that may reload the configuration: the server's own.
	// Zero means nobody may.
	Admin int

	mu sync.RWMutex
}

// SetConfig replaces the configuration; calls already running are not affected.
// A proxy whose Minted is nil keeps the minted values it has now, provided it
// still runs the same program.
func (s *Server) SetConfig(c Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]Spec, len(c.Proxies))
	for name, spec := range c.Proxies {
		if old, ok := s.Config.Proxies[name]; ok && spec.Minted == nil && old.Exec == spec.Exec {
			spec.Minted = old.Minted
		}
		next[name] = spec
	}
	c.Proxies = next
	s.Config = c
}

func (s *Server) config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Config
}

type peerKey struct{}

type peer struct {
	uid int
	err error
}

// ConnContext records who is on the other end of each connection.
func (s *Server) ConnContext(ctx context.Context, c net.Conn) context.Context {
	uid, err := s.Peer(c)
	return context.WithValue(ctx, peerKey{}, peer{uid, err})
}

// Handler serves GET /v1/proxies and POST /v1/run/<name>.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/proxies", s.list)
	mux.HandleFunc("POST /v1/run/{name}", s.run)
	mux.HandleFunc("POST /v1/reload", s.reload)
	return mux
}

func (s *Server) caller(r *http.Request) ([]string, error) {
	p, _ := r.Context().Value(peerKey{}).(peer)
	if p.err != nil {
		return nil, fmt.Errorf("cannot tell who is calling: %w", p.err)
	}
	return s.config().Users[p.uid], nil
}

// workDir picks the directory a call runs in: the caller's, if the proxy asks
// for it and it is inside one of the caller's roots, else "" for a private one.
func (s *Server) workDir(r *http.Request, spec Spec, cwd string) (string, error) {
	if !spec.InWorkspace {
		return "", nil
	}
	p, _ := r.Context().Value(peerKey{}).(peer)
	return Within(s.config().Roots[p.uid], cwd)
}

// Within returns dir with symlinks resolved if it lies inside one of roots.
// A relative path, a path that climbs out, and a symlink that leads out are
// all refused.
func Within(roots []string, dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("refused: the working directory must be an absolute path")
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("refused: working directory: %w", err)
	}
	for _, root := range roots {
		rr, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if real == rr || strings.HasPrefix(real, rr+string(filepath.Separator)) {
			return real, nil
		}
	}
	return "", errors.New("refused: the working directory is outside your workspace")
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	names, err := s.caller(r)
	if err != nil {
		reply(w, http.StatusForbidden, RunResponse{Error: err.Error()})
		return
	}
	cfg := s.config()
	out := []Listing{}
	for _, n := range names {
		if p, ok := cfg.Proxies[n]; ok {
			out = append(out, Listing{Name: n, AgentCommand: p.AgentCommand, Description: p.Description})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	reply(w, http.StatusOK, out)
}

// reload swaps in a new configuration. Only the server's own user may ask, so
// the agent cannot define its own proxy.
func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	p, _ := r.Context().Value(peerKey{}).(peer)
	if p.err != nil || s.Admin == 0 || p.uid != s.Admin {
		reply(w, http.StatusForbidden, RunResponse{Error: "only the proxy's own user may reload it"})
		return
	}
	b, err := ReadBootstrap(http.MaxBytesReader(w, r.Body, maxBootstrap))
	if err == nil {
		var cfg Config
		if cfg, err = b.Config(); err == nil {
			s.SetConfig(cfg)
			reply(w, http.StatusOK, RunResponse{})
			return
		}
	}
	reply(w, http.StatusBadRequest, RunResponse{Error: err.Error()})
}

// maxRequest bounds a request body: arguments plus stdin.
const maxRequest = 4 << 20

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	allowed, err := s.caller(r)
	if err != nil {
		reply(w, http.StatusForbidden, RunResponse{Error: err.Error()})
		return
	}
	spec, ok := s.config().Proxies[name]
	if !ok || !hasName(allowed, name) {
		reply(w, http.StatusForbidden, RunResponse{Error: fmt.Sprintf("proxy %q is not available to you", name)})
		return
	}
	var req RunRequest
	body := http.MaxBytesReader(w, r.Body, maxRequest)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		code := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			code = http.StatusRequestEntityTooLarge
		}
		reply(w, code, RunResponse{Error: "bad request: " + err.Error()})
		return
	}
	if err := Check(spec.Deny, req.Args); err != nil {
		reply(w, http.StatusForbidden, RunResponse{Error: err.Error()})
		return
	}
	dir, err := s.workDir(r, spec, req.Cwd)
	if err != nil {
		reply(w, http.StatusForbidden, RunResponse{Error: err.Error()})
		return
	}
	res, err := s.exec(r.Context(), spec, req, dir)
	if err != nil {
		reply(w, http.StatusBadGateway, RunResponse{Error: err.Error()})
		return
	}
	reply(w, http.StatusOK, res)
}

func hasName(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// exec runs the command with the proxy's environment and nothing else. There
// is no shell: the arguments reach the program as they are.
func (s *Server) exec(ctx context.Context, spec Spec, req RunRequest, workdir string) (RunResponse, error) {
	// HOME and TMPDIR are private to the call, so nothing one call leaves
	// behind (a CLI's cache, say) reaches another, whatever profile it is from.
	private, err := os.MkdirTemp(s.Home, "call-")
	if err != nil {
		return RunResponse{}, err
	}
	defer os.RemoveAll(private)
	dir := workdir
	if dir == "" {
		dir = private
	}

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	if !filepath.IsAbs(spec.Exec) {
		return RunResponse{}, fmt.Errorf("cannot run %q: the path must be absolute", spec.Exec)
	}
	cmd := exec.CommandContext(ctx, spec.Exec, req.Args...)
	cmd.Dir = dir
	cmd.Env = s.environ(spec, private)
	cmd.Stdin = bytes.NewReader(req.Stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, errw := &capped{max: s.MaxOutput}, &capped{max: s.MaxOutput}
	cmd.Stdout, cmd.Stderr = out, errw

	res := RunResponse{}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			res.TimedOut, res.Exit = true, -1
		case errors.As(err, &ee):
			res.Exit = ee.ExitCode()
		default:
			return RunResponse{}, fmt.Errorf("cannot run %s: %w", spec.Exec, err)
		}
	}
	res.Stdout, res.Stderr = out.buf.Bytes(), errw.buf.Bytes()
	res.Truncated = out.over || errw.over
	return res, nil
}

func (s *Server) environ(spec Spec, private string) []string {
	path := spec.Path
	if path == "" {
		path = s.Path
	}
	env := []string{"PATH=" + path, "HOME=" + private, "TMPDIR=" + private, "LANG=C.UTF-8"}
	merged := make(map[string]string, len(spec.Env)+len(spec.Minted))
	for k, v := range spec.Env {
		merged[k] = v
	}
	for k, v := range spec.Minted {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+merged[k])
	}
	return env
}

// capped keeps at most max bytes and drops the rest, so a command cannot fill
// memory by printing forever.
type capped struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	room := c.max - c.buf.Len()
	if room < len(p) {
		c.over = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

var _ io.Writer = (*capped)(nil)

// GroupWritable makes files the server's commands create writable by their
// group, so the agent's user can edit what a proxied command wrote into the
// workspace. It changes the whole process, so call it once, at start-up.
func GroupWritable() { syscall.Umask(0o002) }
