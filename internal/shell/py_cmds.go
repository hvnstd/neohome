package shell

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

// python3 / python — the ".py frontend" (§9's installed software rule). The
// command compiles a small Python subset to game bytecode (core.CompilePy) and
// runs it through the same VM as brun, so file/socket capability gates and
// fuel metering apply unchanged. It is installed software: without the python3
// package's /usr/bin/python3 the shell answers `command not found`.
//
// The subset is documented on core.CompilePy: integer variables, print of an
// int or a string literal, if/elif/else, while, and + - * / %.

func init() {
	builtinTable["python3"] = cmdPython
	builtinTable["python"] = cmdPython
}

func cmdPython(s *Shell, args []string) int {
	// pkgCommandMissing("python3") also gates the bare `python` alias here:
	// the dispatch only knows the installed file is /usr/bin/python3.
	if s.pkgCommandMissing("python3") {
		fmt.Fprintf(s.Out, "%s: python3: command not found\r\n", s.Dev.Hostname)
		return 127
	}
	file := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			s.errf("usage: python3 FILE")
			return 2
		}
		if file != "" {
			s.errf("usage: python3 FILE")
			return 2
		}
		file = a
	}
	if file == "" {
		s.errf("usage: python3 FILE")
		return 2
	}
	data, exists, allowed := s.Dev.FS.ReadPathAs(s.abs(file), s.User)
	if !exists {
		s.errf("python3: can't open file '%s': [Errno 2] No such file or directory", file)
		return 2
	}
	if !allowed {
		s.noteDenied(file, "read")
		s.errf("python3: can't open file '%s': [Errno 13] Permission denied", file)
		return 2
	}
	code, err := core.CompilePy(string(data))
	if err != nil {
		// the error already reads py:<line>: <reason>, like the assembler
		s.errf("%v", err)
		return 1
	}
	out, rc := core.BCRun(s.W, s.Dev, s.User, code, 0)
	for _, l := range out {
		fmt.Fprintln(s.Out, l)
	}
	if rc != 0 {
		return 1
	}
	return 0
}
