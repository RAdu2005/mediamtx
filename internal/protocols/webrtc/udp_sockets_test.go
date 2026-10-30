package webrtc

import (
	"net"
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUDPSockets(t *testing.T) {
	s, err := ListenUDPSockets("udp4", "127.0.0.1:0", 4, 1<<20, 1<<20)
	require.NoError(t, err)
	defer s.Close()

	if runtime.GOOS == "linux" {
		require.Equal(t, 4, s.Len())
	}
	port := s.LocalAddr().(*net.UDPAddr).Port

	// Many senders: whichever socket the kernel would pick, every packet
	// arrives on the first one.
	var clients []*net.UDPConn
	for range 16 {
		c, err2 := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		require.NoError(t, err2)
		defer c.Close()
		clients = append(clients, c)
		_, err2 = c.WriteToUDP([]byte("ping"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		require.NoError(t, err2)
	}

	require.NoError(t, s.SetReadDeadline(time.Now().Add(2*time.Second)))
	senders := map[netip.AddrPort]bool{}
	buf := make([]byte, 1500)
	for range clients {
		n, from, err2 := s.ReadFromAddrPort(buf)
		require.NoError(t, err2)
		require.Equal(t, "ping", string(buf[:n]))
		senders[from] = true
	}
	require.Len(t, senders, len(clients))

	// Replies, written in parallel, leave from the shared port, in order for
	// each destination.
	const perClient = 50
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			to := c.LocalAddr().(*net.UDPAddr).AddrPort()
			for i := range perClient {
				_, err2 := s.WriteToAddrPort([]byte{byte(i)}, to)
				require.NoError(t, err2)
			}
		}()
	}
	wg.Wait()

	for _, c := range clients {
		require.NoError(t, c.SetReadDeadline(time.Now().Add(2*time.Second)))
		for i := range perClient {
			n, from, err2 := c.ReadFromUDPAddrPort(buf)
			require.NoError(t, err2)
			require.Equal(t, 1, n)
			require.Equal(t, byte(i), buf[0])
			require.Equal(t, uint16(port), from.Port())
		}
	}
}

func TestUDPSocketsOne(t *testing.T) {
	s, err := ListenUDPSockets("udp4", "127.0.0.1:0", 0, 0, 0)
	require.NoError(t, err)
	defer s.Close()
	require.Equal(t, 1, s.Len())

	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer c.Close()

	_, err = s.WriteTo([]byte("hi"), c.LocalAddr())
	require.NoError(t, err)
	buf := make([]byte, 10)
	require.NoError(t, c.SetReadDeadline(time.Now().Add(2*time.Second)))
	n, _, err := c.ReadFrom(buf)
	require.NoError(t, err)
	require.Equal(t, "hi", string(buf[:n]))
}
