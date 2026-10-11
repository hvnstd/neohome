package shell

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// The terminal a session is attached to.
//
// Every door gives a session a size — telnet and ssh assume the classic 80x24,
// the browser's terminal reports its real one and updates it when the window is
// resized. That size lives in the session's environment (COLUMNS/LINES) because
// the environment is where a session's own state belongs, and `stty`/`tty` are
// how a person reads it back. Nothing here is invented for the game's benefit:
// a program that writes exactly 80 columns wide should see the same number the
// person at the window sees.
// ---------------------------------------------------------------------------

func init() {
	builtinTable["stty"] = cmdStty
	builtinTable["tty"] = cmdTty
}

func ttySize(s *Shell) (rows, cols int) {
	rows, cols = 24, 80
	if v, err := strconv.Atoi(strings.TrimSpace(s.Env["LINES"])); err == nil && v > 0 {
		rows = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(s.Env["COLUMNS"])); err == nil && v > 0 {
		cols = v
	}
	return rows, cols
}

// SetTTYSize is how a door tells a session the size of the terminal at the
// other end. It is the one piece of PTY state this world keeps, and it is kept
// where the rest of a session's state is.
func (s *Shell) SetTTYSize(cols, rows int) {
	if s.Env == nil {
		return
	}
	if cols > 0 {
		s.Env["COLUMNS"] = strconv.Itoa(cols)
	}
	if rows > 0 {
		s.Env["LINES"] = strconv.Itoa(rows)
	}
}

func cmdTty(s *Shell, args []string) int {
	// a login session is always on a pty in this world: no tty, no login
	fmt.Fprintln(s.Out, "/dev/pts/0")
	return 0
}

func cmdStty(s *Shell, args []string) int {
	rows, cols := ttySize(s)
	if len(args) == 0 {
		fmt.Fprintf(s.Out, "speed 38400 baud; rows %d; columns %d;\n", rows, cols)
		fmt.Fprintln(s.Out, "intr = ^C; quit = ^\\; erase = ^?; kill = ^U; eof = ^D; eol = <undef>;")
		return 0
	}
	switch args[0] {
	case "size":
		if len(args) > 1 && args[1] == "-a" {
			cmdStty(s, nil)
			return 0
		}
		// the kernel's own order: rows then columns
		fmt.Fprintf(s.Out, "%d %d\n", rows, cols)
		return 0
	case "-a":
		fallthrough
	case "--all":
		fmt.Fprintf(s.Out, "speed 38400 baud; rows %d; columns %d; line = 0;\n", rows, cols)
		fmt.Fprint(s.Out, "intr = ^C; quit = ^\\; erase = ^?; kill = ^U; eof = ^D; eol = <undef>;\n"+
			"eol2 = <undef>; swtch = <undef>; start = ^Q; stop = ^S; susp = ^Z; rprnt = ^R;\n"+
			"werase = ^W; lnext = ^V; discard = ^O; min = 1; time = 0;\n"+
			"-parenb -parodd -cmspar cs8 -hupcl -cstopb cread -clocal -crtscts\n"+
			"-ignbrk -brkint -ignpar -parmrk -inpck -istrip -inlcr -igncr icrnl ixon -ixoff\n"+
			"-iuclc -ixany -imaxbel iutf8\n"+
			"opost -olcuc -ocrnl onlcr -onocr -onlret -ofill -ofdel nl0 cr0 tab0 bs0 vt0 ff0\n"+
			"isig icanon iexten echo echoe echok -echonl -noflsh -xcase -tostop -echoprt\n")
		return 0
	case "rows", "cols", "columns":
		if len(args) < 2 {
			if args[0] == "rows" {
				fmt.Fprintln(s.Out, rows)
			} else {
				fmt.Fprintln(s.Out, cols)
			}
			return 0
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n <= 0 {
			fmt.Fprintf(s.Out, "stty: invalid argument %q\n", args[1])
			return 1
		}
		if args[0] == "rows" {
			s.SetTTYSize(0, n)
		} else {
			s.SetTTYSize(n, 0)
		}
		return 0
	default:
		fmt.Fprintf(s.Out, "stty: invalid argument %q\n", args[0])
		return 1
	}
}
