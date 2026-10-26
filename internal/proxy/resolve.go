package proxy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// socketsIn lists the proxy socket paths under root, in name order.
func socketsIn(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(root, e.Name(), "sock"))
	}
	sort.Strings(out)
	return out
}

// Resolve finds the proxy the caller may use for command, the name the agent
// typed. It asks each server what the caller may use, so a proxy that belongs
// to another profile is never found and never named. It returns the proxy's
// name and a client for its server.
func Resolve(root, command string) (string, *Client, error) {
	for _, sock := range socketsIn(root) {
		cl := &Client{Socket: sock}
		list, err := cl.List(context.Background())
		if err != nil {
			continue // a server that is down has nothing to offer
		}
		for _, l := range list {
			if l.AgentCommand == command {
				return l.Name, cl, nil
			}
		}
	}
	return "", nil, fmt.Errorf("the command %q is not available to you through a proxy", command)
}

// ListAll returns every proxy the caller may use, by agent command.
func ListAll(root string) []Listing {
	all := []Listing{}
	for _, sock := range socketsIn(root) {
		list, err := (&Client{Socket: sock}).List(context.Background())
		if err != nil {
			continue
		}
		all = append(all, list...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].AgentCommand < all[j].AgentCommand })
	return all
}

// Up reports whether a server answers on socket. A server answers a listing
// to anyone, even a caller with no proxies, so this needs no permission.
func Up(socket string) error {
	_, err := (&Client{Socket: socket}).List(context.Background())
	return err
}
