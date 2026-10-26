package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
)

// Bootstrap is what the host sends the server on stdin. It is the only way
// secrets reach the container: they are never written to its disk.
type Bootstrap struct {
	Proxies map[string]Spec     `json:"proxies"`
	Users   map[string][]string `json:"users"` // uid -> proxy names
	Roots   map[string][]string `json:"roots"` // uid -> directories its workspace calls may run in
}

// maxBootstrap bounds the document the server reads.
const maxBootstrap = 4 << 20

// ReadBootstrap reads one Bootstrap document.
func ReadBootstrap(r io.Reader) (Bootstrap, error) {
	var b Bootstrap
	lr := io.LimitReader(r, maxBootstrap+1)
	if err := json.NewDecoder(lr).Decode(&b); err != nil {
		return Bootstrap{}, fmt.Errorf("bootstrap: %w", err)
	}
	return b, nil
}

// Config turns the document into the server's configuration.
func (b Bootstrap) Config() (Config, error) {
	cfg := Config{Proxies: b.Proxies, Users: map[int][]string{}, Roots: map[int][]string{}}
	if cfg.Proxies == nil {
		cfg.Proxies = map[string]Spec{}
	}
	for u, names := range b.Users {
		uid, err := strconv.Atoi(u)
		if err != nil {
			return Config{}, fmt.Errorf("bootstrap: user %q is not a uid", u)
		}
		cfg.Users[uid] = names
	}
	for u, dirs := range b.Roots {
		uid, err := strconv.Atoi(u)
		if err != nil {
			return Config{}, fmt.Errorf("bootstrap: user %q is not a uid", u)
		}
		cfg.Roots[uid] = dirs
	}
	return cfg, nil
}

// Listen opens the server's Unix socket. The socket is mode 0660 and owned by
// gid, so only members of that group can connect; the kernel checks that. A
// file left at path by an earlier run is removed first.
func Listen(path string, gid int) (net.Listener, error) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chown(path, -1, gid); err != nil {
		ln.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
