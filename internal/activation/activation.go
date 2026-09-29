package activation

import (
	"net"
	"os"
	"strconv"
)

// Listeners returns file descriptors inherited from systemd socket activation.
// If not running under systemd or no fds were passed, it returns nil.
//
// Systemd sets LISTEN_PID and LISTEN_FDS when passing file descriptors.
// The fds start at fd 3 (SD_LISTEN_FDS_START).
func Listeners() []net.Listener {
	pid := os.Getenv("LISTEN_PID")
	if pid == "" {
		return nil
	}

	expected, err := strconv.Atoi(pid)
	if err != nil || expected != os.Getpid() {
		return nil
	}

	nfds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || nfds == 0 {
		return nil
	}

	var listeners []net.Listener
	for i := 0; i < nfds; i++ {
		fd := uintptr(3 + i)
		f := os.NewFile(fd, "")
		if f == nil {
			continue
		}
		ln, err := net.FileListener(f)
		f.Close()
		if err != nil {
			continue
		}
		listeners = append(listeners, ln)
	}

	return listeners
}
