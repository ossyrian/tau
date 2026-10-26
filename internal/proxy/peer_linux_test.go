//go:build linux

package proxy

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPeerUIDIsTheCallersOwn(t *testing.T) {
	dir, _ := os.MkdirTemp("", "pu")
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- -2
			return
		}
		uid, err := PeerUID(c)
		if err != nil {
			uid = -1
		}
		got <- uid
	}()
	c, err := net.Dial("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if uid := <-got; uid != os.Getuid() {
		t.Errorf("peer uid %d, want %d", uid, os.Getuid())
	}
}
