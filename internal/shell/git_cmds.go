package shell

// git — the client for the world's real repositories. A clone copies the
// server's objects over the network, a commit is real objects in the local
// .git, a push is an authenticated write against the server's account
// records. Everything git prints here is derived from that state.

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["git"] = cmdGit
}

func cmdGit(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: git clone <url> [dir] | git status | git log | git commit -m <msg> | git pull | git push")
		return 1
	}
	switch args[0] {
	case "clone":
		return gitClone(s, args[1:])
	case "status":
		return gitStatus(s)
	case "log":
		return gitLogCmd(s)
	case "commit":
		return gitCommit(s, args[1:])
	case "pull":
		return gitPull(s)
	case "push":
		return gitPush(s)
	}
	s.errf("git: '%s' is not a git command (clone, status, log, commit, pull, push)", args[0])
	return 1
}

func gitClone(s *Shell, args []string) int {
	if len(args) < 1 {
		s.errf("usage: git clone <url> [dir]")
		return 1
	}
	url := args[0]
	target := ""
	if len(args) > 1 {
		target = s.abs(args[1])
	} else {
		// no directory given: the repo's own name under the CWD
		name, err := core.GitRepoNameFrom(url)
		if err != nil {
			s.errf("%v", err)
			return 1
		}
		target = s.CWD + "/" + name
	}
	_, err := s.W.GitClone(s.Dev, url, target, s.User)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "Cloning into '%s'...\ndone.\n", target)
	return 0
}

func gitStatus(s *Shell) int {
	deltas, err := s.W.GitStatus(s.Dev, s.CWD, s.User)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	fmt.Fprintln(s.Out, "On branch main")
	if len(deltas) == 0 {
		fmt.Fprintln(s.Out, "nothing to commit, working tree clean")
		return 0
	}
	changed := 0
	fmt.Fprintln(s.Out, "Changes not staged for commit:")
	for _, d := range deltas {
		if d.State == "added" {
			continue
		}
		fmt.Fprintf(s.Out, "\t%s:  %s\n", d.State, d.Path)
		changed++
	}
	var untracked []string
	for _, d := range deltas {
		if d.State == "added" {
			untracked = append(untracked, d.Path)
		}
	}
	if len(untracked) > 0 {
		fmt.Fprintln(s.Out, "Untracked files:")
		for _, p := range untracked {
			fmt.Fprintf(s.Out, "\t%s\n", p)
		}
	}
	if changed == 0 {
		fmt.Fprintf(s.Out, "\n%d new file(s) to commit (commit -m adds everything)\n", len(untracked))
	}
	return 0
}

func gitLogCmd(s *Shell) int {
	log, err := s.W.GitLog(s.Dev, s.CWD)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	for _, c := range log {
		fmt.Fprintf(s.Out, "commit %s\nAuthor: %s\nDate:   %s\n\n    %s\n", c.ID, c.Author, c.Date, c.Message)
	}
	return 0
}

func gitCommit(s *Shell, args []string) int {
	msg := ""
	for i, a := range args {
		if (a == "-m" || a == "--message") && i+1 < len(args) {
			msg = args[i+1]
		}
	}
	if msg == "" {
		s.errf("usage: git commit -m <message>")
		return 1
	}
	c, err := s.W.GitCommit(s.Dev, s.CWD, s.User, msg)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "[main %s] %s\n", c.ID[:8], c.Message)
	return 0
}

func gitPull(s *Shell) int {
	res, err := s.W.GitPull(s.Dev, s.CWD, s.User)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	fmt.Fprintln(s.Out, res)
	return 0
}

func gitPush(s *Shell) int {
	// a push is a write on the server: it wants the account's real password
	fmt.Fprintf(s.Out, "password for %s: ", s.User.Name)
	pass := s.ReadPasswordLine("")
	res, err := s.W.GitPush(s.Dev, s.CWD, s.User, s.User.Name, pass)
	if err != nil {
		s.errf("%v", err)
		return 1
	}
	fmt.Fprint(s.Out, res)
	if !strings.HasSuffix(res, "\n") {
		fmt.Fprintln(s.Out)
	}
	return 0
}
