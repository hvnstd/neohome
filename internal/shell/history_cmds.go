package shell

import (
	"fmt"
	"strings"
)

// Shell history.
//
// A shell keeps its history in a real file on the device — "~/.bash_history" for
// bash, "~/.ash_history" for ash, overridable with HISTFILE. That file is world
// state, and that matters for the whole game:
//
//	defence  the trail on a compromised box is how you work out what was done
//	attack   the trail is also the thing that convicts you, so it must be cleared
//	honesty  the history belongs to the account, so the account can rewrite it —
//	         but doing so is its own recorded act
//
// Session history (the `history` command in the current shell) is separate: it is
// the lines typed here, and bash merges it into the file when the shell exits.

// HistoryFile resolves the file this account's history belongs in.
func (s *Shell) HistoryFile() string {
	if s.User == nil {
		return ""
	}
	if hf := s.Env["HISTFILE"]; hf != "" {
		return s.abs(hf)
	}
	if s.User.HistFile != "" {
		return s.User.HistFile
	}
	// the shell's own default: dot-file named after the shell
	// NOTE: check bash before ash — "/bin/bash" contains the substring "ash".
	name := "bash_history"
	sh := s.User.Shell
	switch {
	case strings.Contains(sh, "bash"):
		name = "bash_history"
	case strings.Contains(sh, "zsh"):
		name = "zsh_history"
	case strings.Contains(sh, "ash") || strings.Contains(sh, "busybox"):
		name = "ash_history"
	}
	home := s.User.Home
	if home == "" {
		home = s.CWD
	}
	return s.abs(home + "/." + name)
}

// loadHistoryOnce makes the one-time history load happen before the first
// prompt, where a real shell does it.
func (s *Shell) loadHistoryOnce() {
	if s.histLoaded {
		return
	}
	s.histLoaded = true
	s.loadHistory()
}

// recordHistory appends one line to the session's history and to the file, the
// way a real shell does as you work.
func (s *Shell) recordHistory(line string) {
	s.hist = append(s.hist, line)
	s.appendHistoryFile(line)
}

// appendHistoryFile writes the line to the account's history file on the device.
// Without a filesystem (fictional/infra nodes) there is simply nowhere to put it.
func (s *Shell) appendHistoryFile(line string) {
	if s.Dev == nil || s.Dev.FS == nil || s.User == nil {
		return
	}
	hf := s.HistoryFile()
	if hf == "" {
		return
	}
	// HISTCONTROL=ignorespace / ignoredups are real behaviours worth honoring
	if s.Env["HISTCONTROL"] != "" {
		ctl := s.Env["HISTCONTROL"]
		if strings.Contains(ctl, "ignorespace") && strings.HasPrefix(line, " ") {
			return
		}
		if strings.Contains(ctl, "ignoredups") && len(s.hist) >= 2 && s.hist[len(s.hist)-2] == line {
			return
		}
	}
	if line == "" {
		return
	}
	prev, ok := s.Dev.FS.Read(hf)
	if !ok {
		s.Dev.FS.Write(hf, line+"\n", 0600, s.User.Name, s.User.Name)
		return
	}
	// strip HISTFILE / HISTSIZE assignments: bash does not store them
	if strings.HasPrefix(line, "HISTFILE=") || strings.HasPrefix(line, "HISTSIZE=") ||
		strings.HasPrefix(line, "HISTCONTROL=") {
		return
	}
	// bash also drops most leading-space lines when ignoreboth is set; otherwise
	// the line is simply appended
	content := string(prev)
	// the file is capped, like HISTFILESIZE — a huge file is itself suspicious
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	max := 500
	if hs := s.Env["HISTFILESIZE"]; hs != "" {
		if n := atoiSafe(hs); n > 0 {
			max = n
		}
	}
	if len(lines) >= max {
		lines = lines[len(lines)-max+1:]
		content = strings.Join(lines, "\n") + "\n"
	}
	s.Dev.FS.Write(hf, content+line+"\n", 0600, s.User.Name, s.User.Name)
}

// cmdHistory prints the session history, or with `-c` clears it. `history -c`
// clears only the in-memory list on a real shell; clearing the FILE is what
// really removes the trail, and that is `history -w`/`rm` — so `-c` here clears
// memory and the file only when the player asks for it explicitly.
func cmdHistory(s *Shell, args []string) int {
	clearFile := false
	writeFile := false
	for _, a := range args {
		switch a {
		case "-c":
			clearFile = true
		case "-w":
			writeFile = true
		}
	}
	if clearFile {
		s.hist = nil
		if s.User != nil {
			s.W.Record("forensics", s.User.Name, s.Dev.SourceIPFor(s.Dev), s.Dev.ID,
				"cleared shell history (session)", 2)
			s.Dev.Logf("warn", "audit", "%s cleared its shell history", s.User.Name)
		}
		fmt.Fprintln(s.Out, "history cleared")
		return 0
	}
	if writeFile {
		// flush memory into the file (what bash does on exit)
		if s.Dev != nil && s.Dev.FS != nil {
			hf := s.HistoryFile()
			s.Dev.FS.Write(hf, strings.Join(s.hist, "\n")+"\n", 0600, s.User.Name, s.User.Name)
			fmt.Fprintf(s.Out, "history written to %s\n", hf)
		}
		return 0
	}
	for i, h := range s.hist {
		fmt.Fprintf(s.Out, "%4d  %s\n", i+1, h)
	}
	return 0
}
