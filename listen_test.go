package main

import (
	"net"
	"strconv"
	"testing"
)

func TestListenCueviaUsesAnotherPortWhenOccupied(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	listener, err := listenCuevia(occupied.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listener.Addr().String() == occupied.Addr().String() {
		t.Fatal("reused an occupied port")
	}
	host, _, _ := net.SplitHostPort(listener.Addr().String())
	if host != "127.0.0.1" {
		t.Fatalf("unexpected listen host: %s", host)
	}
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
}

func TestListenCueviaFallsBackAfterTwentyOccupiedPorts(t *testing.T) {
	var reserved []net.Listener
	defer func() {
		for _, listener := range reserved {
			listener.Close()
		}
	}()
	for port := 42000; port < 60000; port += 20 {
		for _, listener := range reserved {
			listener.Close()
		}
		reserved = nil
		for next := port; next < port+20; next++ {
			listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(next)))
			if err != nil {
				break
			}
			reserved = append(reserved, listener)
		}
		if len(reserved) != 20 {
			continue
		}
		listener, err := listenCuevia(reserved[0].Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		for _, occupied := range reserved {
			if listener.Addr().String() == occupied.Addr().String() {
				t.Fatal("fallback reused an occupied port")
			}
		}
		return
	}
	t.Fatal("could not reserve a contiguous range for the fallback test")
}
