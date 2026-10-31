//go:build !linux

package webrtc

import "net"

// listenUDPSockets opens a single socket: steering incoming packets to one
// socket of a SO_REUSEPORT group needs Linux.
func listenUDPSockets(network, address string, _ int) ([]*net.UDPConn, error) {
	c, err := net.ListenPacket(network, address)
	if err != nil {
		return nil, err
	}
	return []*net.UDPConn{c.(*net.UDPConn)}, nil
}
