package main

import (
	"net"
	"strconv"
)

// Bind the real listener before announcing its URL; no probe-and-release race.
func listenCuevia(addr string) (net.Listener, error) {
	host, textPort, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(textPort)
	if err != nil || port < 0 || port > 65535 {
		return net.Listen("tcp", addr)
	}
	if port == 0 || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return net.Listen("tcp", addr)
	}
	for next := port; next < port+20 && next <= 65535; next++ {
		listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(next)))
		if err == nil {
			return listener, nil
		}
	}
	return net.Listen("tcp", net.JoinHostPort(host, "0"))
}
