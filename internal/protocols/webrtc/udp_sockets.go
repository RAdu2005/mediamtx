package webrtc

import (
	"errors"
	"hash/maphash"
	"net"
	"net/netip"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/readbuffer"
)

// UDPSockets is a UDP listener made of several sockets bound to the same
// address, so that sessions sharing it can send in parallel.
//
// All sessions of the ICE UDP mux share one socket, and the kernel and Go
// serialize writes on a socket: with many readers, every packet waits for
// the previous one's sendto, which caps the output of the whole server well
// below what the CPU and the network allow. With SO_REUSEPORT, the extra
// sockets share the address, so packets leave from the same port whichever
// socket sends them. Each destination always uses the same socket, which
// keeps its packets in order. A reuseport program steers every incoming
// packet to the first socket, so reading is unchanged.
type UDPSockets struct {
	conns []*net.UDPConn
	seed  maphash.Seed
}

var (
	_ net.PacketConn = (*UDPSockets)(nil)
	// pion/ice uses this allocation-free interface when available.
	_ interface {
		ReadFromAddrPort(b []byte) (int, netip.AddrPort, error)
		WriteToAddrPort(b []byte, addr netip.AddrPort) (int, error)
	} = (*UDPSockets)(nil)
)

// ListenUDPSockets opens n sockets on address (n <= 1, or a system without
// SO_REUSEPORT, gives one) with the given buffer sizes (0: system default).
func ListenUDPSockets(network, address string, n int, readBufferSize, writeBufferSize int) (*UDPSockets, error) {
	conns, err := listenUDPSockets(network, address, max(n, 1))
	if err != nil {
		return nil, err
	}

	s := &UDPSockets{conns: conns, seed: maphash.MakeSeed()}

	for _, c := range conns {
		if readBufferSize != 0 {
			if err = readbuffer.SetReadBuffer(c, readBufferSize); err != nil {
				s.Close()
				return nil, err
			}
		}
		if writeBufferSize != 0 {
			if err = c.SetWriteBuffer(writeBufferSize); err != nil {
				s.Close()
				return nil, err
			}
		}
	}

	return s, nil
}

// Len returns the number of sockets.
func (s *UDPSockets) Len() int {
	return len(s.conns)
}

func (s *UDPSockets) socketFor(addr netip.AddrPort) *net.UDPConn {
	if len(s.conns) == 1 {
		return s.conns[0]
	}
	var h maphash.Hash
	h.SetSeed(s.seed)
	b := addr.Addr().As16()
	h.Write(b[:])
	port := addr.Port()
	h.Write([]byte{byte(port >> 8), byte(port)})
	return s.conns[h.Sum64()%uint64(len(s.conns))]
}

// ReadFromAddrPort reads a packet. Every incoming packet arrives on the
// first socket.
func (s *UDPSockets) ReadFromAddrPort(b []byte) (int, netip.AddrPort, error) {
	return s.conns[0].ReadFromUDPAddrPort(b)
}

// WriteToAddrPort writes a packet to addr from the socket of addr.
func (s *UDPSockets) WriteToAddrPort(b []byte, addr netip.AddrPort) (int, error) {
	return s.socketFor(addr).WriteToUDPAddrPort(b, addr)
}

// ReadFrom implements net.PacketConn.
func (s *UDPSockets) ReadFrom(b []byte) (int, net.Addr, error) {
	return s.conns[0].ReadFrom(b)
}

// WriteTo implements net.PacketConn.
func (s *UDPSockets) WriteTo(b []byte, addr net.Addr) (int, error) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok {
		return 0, errors.New("not a UDP address")
	}
	return s.WriteToAddrPort(b, ua.AddrPort())
}

// Close closes every socket.
func (s *UDPSockets) Close() error {
	var errs []error
	for _, c := range s.conns {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

// LocalAddr returns the address all sockets are bound to.
func (s *UDPSockets) LocalAddr() net.Addr {
	return s.conns[0].LocalAddr()
}

// SetDeadline implements net.PacketConn.
func (s *UDPSockets) SetDeadline(t time.Time) error {
	return s.forEach(func(c *net.UDPConn) error { return c.SetDeadline(t) })
}

// SetReadDeadline implements net.PacketConn.
func (s *UDPSockets) SetReadDeadline(t time.Time) error {
	return s.conns[0].SetReadDeadline(t)
}

// SetWriteDeadline implements net.PacketConn.
func (s *UDPSockets) SetWriteDeadline(t time.Time) error {
	return s.forEach(func(c *net.UDPConn) error { return c.SetWriteDeadline(t) })
}

func (s *UDPSockets) forEach(f func(*net.UDPConn) error) error {
	for _, c := range s.conns {
		if err := f(c); err != nil {
			return err
		}
	}
	return nil
}
