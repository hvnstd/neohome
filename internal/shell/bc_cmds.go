package shell

import (
	"fmt"
	"strconv"
	"strings"

	"neohome/internal/core"
)

// brun runs game bytecode: assembled in memory from a .basm file (errors
// carry line numbers), executed inline with a fuel cap, or backgrounded by
// the program itself through SPAWN. Programs read and write this device's
// files as the invoking user, dial through the same packet path as
// everything else, and die on fuel instead of hanging the world.

func init() {
	builtinTable["brun"] = cmdBrun
}

func cmdBrun(s *Shell, args []string) int {
	file := ""
	fuel := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fuel":
			if i+1 >= len(args) {
				s.errf("usage: brun FILE [--fuel N]")
				return 1
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				s.errf("brun: bad fuel %q", args[i])
				return 1
			}
			fuel = n
		default:
			if strings.HasPrefix(args[i], "-") {
				s.errf("usage: brun FILE [--fuel N]")
				return 1
			}
			if file != "" {
				s.errf("usage: brun FILE [--fuel N]")
				return 1
			}
			file = args[i]
		}
	}
	if file == "" {
		s.errf("usage: brun FILE [--fuel N]")
		return 1
	}
	data, exists, allowed := s.Dev.FS.ReadPathAs(s.abs(file), s.User)
	if !exists {
		s.errf("brun: %s: No such file", file)
		return 1
	}
	if !allowed {
		s.noteDenied(file, "read")
		s.errf("brun: %s: Permission denied", file)
		return 1
	}
	code, err := core.BCAssemble(string(data))
	if err != nil {
		s.errf("brun: %v", err)
		return 1
	}
	out, rc := core.BCRun(s.W, s.Dev, s.User, code, fuel)
	for _, l := range out {
		fmt.Fprintln(s.Out, l)
	}
	if rc != 0 {
		return 1
	}
	return 0
}
