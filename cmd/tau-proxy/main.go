// Command tau-proxy runs inside the tau container. "serve" and "reload" are
// run by the host through docker exec as the server's own user; "call" is what
// the shim scripts run for the agent.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ossyrian/tau/internal/proxy"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tau-proxy:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tau-proxy serve|reload|up NAME, tau-proxy call COMMAND -- ARGS, tau-proxy list")
	}
	switch args[0] {
	case "serve", "reload", "up":
		if len(args) < 2 || !proxy.ValidName(args[1]) {
			return fmt.Errorf("usage: tau-proxy %s NAME", args[0])
		}
		switch args[0] {
		case "serve":
			return serve(args[1])
		case "reload":
			return reload(args[1])
		}
		return proxy.Up(proxy.SocketFor(args[1]))
	case "call":
		return call(args[1:])
	case "list":
		return list()
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func serve(name string) error {
	b, err := proxy.ReadBootstrap(os.Stdin)
	if err != nil {
		return err
	}
	cfg, err := b.Config()
	if err != nil {
		return err
	}
	proxy.GroupWritable()
	home, err := os.MkdirTemp("", "tau-proxy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	ln, err := proxy.Listen(proxy.SocketFor(name), proxy.ClientsGID)
	if err != nil {
		return err
	}
	s := &proxy.Server{
		Config: cfg, Home: home, Path: proxy.ServerPath,
		Timeout: 10 * time.Minute, MaxOutput: 8 << 20,
		Peer: proxy.PeerUID, Admin: os.Getuid(),
	}
	srv := &http.Server{Handler: s.Handler(), ConnContext: s.ConnContext}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		srv.Shutdown(context.Background())
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// reload reads a new document from stdin and sends it to the running server.
// The server accepts it only from its own uid, so this must run as that user.
func reload(name string) error {
	return (&proxy.Client{Socket: proxy.SocketFor(name)}).Reload(context.Background(), os.Stdin)
}

// list prints the proxies the caller may use, as JSON, from every server. It
// holds names, commands and descriptions: never a secret.
func list() error {
	return json.NewEncoder(os.Stdout).Encode(proxy.ListAll(proxy.SocketDir))
}

// call is "tau-proxy call COMMAND -- ARGS...". COMMAND is what the agent
// typed. It finds the one proxy this caller has for it.
func call(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tau-proxy call COMMAND -- ARGS")
	}
	command, rest := args[0], args[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	name, cl, err := proxy.Resolve(proxy.SocketDir, command)
	if err != nil {
		return err
	}
	code, err := cl.Call(context.Background(), name, rest, proxy.StdinFor(os.Stdin), os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil
}
