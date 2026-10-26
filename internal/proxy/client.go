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

	"golang.org/x/term"
)

// Client calls a proxy server over its Unix socket.
type Client struct{ Socket string }

func (c *Client) http() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
		},
		DisableKeepAlives: true,
	}}
}

// Call runs the proxy name with args and copies its output to stdout and
// stderr. It returns the command's exit code. An error means the call did not
// run: the server refused it or could not be reached.
func (c *Client) Call(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cwd, _ := os.Getwd()
	req := RunRequest{Args: args, Cwd: cwd}
	if stdin != nil {
		data, err := io.ReadAll(io.LimitReader(stdin, maxRequest))
		if err != nil {
			return 0, err
		}
		req.Stdin = data
	}
	body, err := json.Marshal(req)
	if err != nil {
		return 0, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://tau/v1/run/"+name, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	resp, err := c.http().Do(hr)
	if err != nil {
		return 0, fmt.Errorf("cannot reach the proxy server at %s (is it running? tau restart): %w", c.Socket, err)
	}
	defer resp.Body.Close()
	var res RunResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*maxRequest)).Decode(&res); err != nil {
		return 0, fmt.Errorf("proxy server answered %s with something unreadable", resp.Status)
	}
	if res.Error != "" {
		return 0, errors.New(res.Error)
	}
	stdout.Write(res.Stdout)
	stderr.Write(res.Stderr)
	if res.TimedOut {
		fmt.Fprintln(stderr, "tau: the proxied command timed out and was stopped")
	}
	if res.Truncated {
		fmt.Fprintln(stderr, "tau: output was truncated")
	}
	return res.Exit, nil
}

// List returns the proxies the caller may use.
func (c *Client) List(ctx context.Context) ([]Listing, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://tau/v1/proxies", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http().Do(hr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the proxy server at %s: %w", c.Socket, err)
	}
	defer resp.Body.Close()
	var out []Listing
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// Reload sends a new Bootstrap document to the server. The server only accepts
// it from its own user.
func (c *Client) Reload(ctx context.Context, doc io.Reader) error {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://tau/v1/reload", io.LimitReader(doc, maxBootstrap))
	if err != nil {
		return err
	}
	resp, err := c.http().Do(hr)
	if err != nil {
		return fmt.Errorf("cannot reach the proxy server at %s: %w", c.Socket, err)
	}
	defer resp.Body.Close()
	var res RunResponse
	json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&res)
	if resp.StatusCode != http.StatusOK {
		if res.Error == "" {
			res.Error = resp.Status
		}
		return errors.New(res.Error)
	}
	return nil
}

// isTerminal is replaced in tests.
var isTerminal = func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

// StdinFor returns what to forward as a call's stdin: the file when it is a
// pipe or a redirect, nothing when it is a terminal. A shim run by hand would
// otherwise wait for the user to end input that they never meant to give.
func StdinFor(f *os.File) io.Reader {
	if f == nil || isTerminal(f) {
		return nil
	}
	return f
}
