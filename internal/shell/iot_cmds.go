package shell

// camera and lock — the front-door IoT pair. Both are real devices on the
// LAN: the commands dial their services like any other client, the camera's
// recordings live on the NAS as files, and the lock's state is world state
// with the PIN inside a root-only config file on the device itself.

import (
	"fmt"
	"strconv"

	"neohome/internal/core"
)

func init() {
	builtinTable["camera"] = cmdCamera
	builtinTable["lock"] = cmdLock
}

func cmdCamera(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	_, rc := dialIoT(s, "cam-front", 554, "camera")
	if rc != 0 {
		return rc
	}
	switch sub {
	case "", "list":
		files, err := s.W.Recordings()
		if err != nil {
			s.errf("camera: %v", err)
			return 1
		}
		if len(files) == 0 {
			fmt.Fprintln(s.Out, "no recordings")
			return 0
		}
		for _, f := range files {
			fmt.Fprintln(s.Out, f)
		}
		fmt.Fprintf(s.Out, "\nview one: camera view N\n")
		return 0
	case "view":
		if len(args) < 2 {
			s.errf("usage: camera view N")
			return 1
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n <= 0 {
			s.errf("camera: bad clip number %q", args[1])
			return 1
		}
		clip, err := s.W.Recording(n)
		if err != nil {
			s.errf("camera: %v", err)
			return 1
		}
		fmt.Fprint(s.Out, clip)
		return 0
	}
	s.errf("usage: camera [list|view N]")
	return 1
}

func cmdLock(s *Shell, args []string) int {
	action := "status"
	code := ""
	if len(args) > 0 {
		action = args[0]
	}
	if len(args) > 1 {
		code = args[1]
	}
	dst, rc := dialIoT(s, "lock-front", 8899, "lock")
	if rc != 0 {
		return rc
	}
	res, err := s.W.LockCommand(s.Dev, s.User, action, code)
	if err != nil {
		s.errf("lock: %v", err)
		return 1
	}
	fmt.Fprintln(s.Out, res)
	_ = dst
	return 0
}

// dialIoT resolves and dials one of the household IoT devices; the
// connection is the gate (power, route, service state, LAN scope).
func dialIoT(s *Shell, host string, port int, tool string) (*core.Device, int) {
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("%s: resolve %s: %s", tool, host, how)
		return nil, 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, port)
	if svc == nil {
		s.errf("%s: connect to %s (%s): %s", tool, host, ip, msg)
		return nil, 1
	}
	return dst, 0
}
