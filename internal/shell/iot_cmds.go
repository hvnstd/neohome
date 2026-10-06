package shell

// camera and lock — the front-door IoT pair. Both are real devices on the
// LAN: the commands dial their services like any other client, the camera's
// recordings live on the NAS as files, and the lock's state is world state
// with the PIN inside a root-only config file on the device itself.

import (
	"fmt"
	"strconv"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["camera"] = cmdCamera
	builtinTable["lock"] = cmdLock
	// camctl lives on the camera itself: it is the vendor's own CLI, the thing
	// an owner (or someone who guessed the default admin password) runs to
	// switch cloud viewing on. §14's "Exposed IoT" starts here.
	builtinTable["camctl"] = cmdCamctl
}

func cmdCamctl(s *Shell, args []string) int {
	if s.Dev.Profile != "iot" {
		s.errf("camctl: this tool runs on the camera")
		return 1
	}
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "status", "":
		state := "off"
		if s.W.CameraCloudOn() {
			state = "on"
		}
		fmt.Fprintf(s.Out, "neoiot camera firmware 3.1\nstream: rtsp://%s:554/stream\ncloud viewing: %s\n",
			s.Dev.FirstLANIP(), state)
		// what the camera is configured to do and what the network actually
		// does are two different things; status must not blur them, because
		// a stale config is exactly how an "off" camera stays on the internet
		mapped := false
		if router := s.W.RouterFor(s.Dev); router != nil {
			for _, l := range router.FW().UPnPLeases {
				if l.IP != s.Dev.FirstLANIP() {
					continue
				}
				mapped = true
				fmt.Fprintf(s.Out, "upstream mapping: %s/%d -> %s:%d (%s)\n",
					forEachProto(l.Proto), l.EPort, l.IP, l.IPort, l.Desc)
			}
		}
		switch {
		case s.W.CameraCloudOn() && !mapped:
			fmt.Fprintln(s.Out, "warning: cloud viewing is configured but the gateway holds no mapping — the stream is not reachable from the internet")
		case !s.W.CameraCloudOn() && mapped:
			fmt.Fprintln(s.Out, "warning: cloud viewing is off but the gateway still forwards the stream")
		}
		return 0
	case "cloud":
		on := true
		if len(args) > 1 {
			on = args[1] == "on" || args[1] == "enable" || args[1] == "1"
		}
		msg, err := s.W.CameraCloud(s.Dev, on)
		if err != nil {
			s.errf("camctl: %v", err)
			return 1
		}
		fmt.Fprintln(s.Out, msg)
		return 0
	}
	s.errf("usage: camctl [status|cloud on|cloud off]")
	return 1
}

func forEachProto(p string) string { return strings.ToUpper(p) }

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
