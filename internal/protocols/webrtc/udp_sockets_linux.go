package webrtc

import (
	"context"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// listenUDPSockets binds n sockets to address with SO_REUSEPORT. A classic
// BPF reuseport program that always returns 0 makes the kernel deliver every
// incoming packet to the first socket of the group, instead of spreading
// them by hash over sockets that are never read.
func listenUDPSockets(network, address string, n int) ([]*net.UDPConn, error) {
	if n == 1 {
		c, err := net.ListenPacket(network, address)
		if err != nil {
			return nil, err
		}
		return []*net.UDPConn{c.(*net.UDPConn)}, nil
	}

	lc := net.ListenConfig{
		Control: func(_, _ string, rc syscall.RawConn) error {
			var serr error
			err := rc.Control(func(fd uintptr) {
				serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
			})
			if err != nil {
				return err
			}
			return serr
		},
	}

	conns := make([]*net.UDPConn, 0, n)
	closeAll := func() {
		for _, c := range conns {
			c.Close()
		}
	}

	for i := range n {
		// the others bind to the first one's address, in case its port was 0.
		addr := address
		if i > 0 {
			addr = conns[0].LocalAddr().String()
		}

		pc, err := lc.ListenPacket(context.Background(), network, addr)
		if err != nil {
			closeAll()
			return nil, err
		}
		c := pc.(*net.UDPConn)
		conns = append(conns, c)

		if i == 0 {
			if err = steerToFirst(c); err != nil {
				closeAll()
				return nil, err
			}
		}
	}

	return conns, nil
}

func steerToFirst(c *net.UDPConn) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	prog := []unix.SockFilter{{Code: unix.BPF_RET | unix.BPF_K, K: 0}}
	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}

	var serr error
	err = rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptSockFprog(int(fd), unix.SOL_SOCKET, unix.SO_ATTACH_REUSEPORT_CBPF, &fprog)
	})
	if err != nil {
		return err
	}
	return serr
}
