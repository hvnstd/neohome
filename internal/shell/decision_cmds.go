package shell

import (
	"fmt"

	"neohome/internal/core"
)

// §21 需决策时唤醒调度: the player's window onto the decision queue.
//
// The rule layer keeps running with no LLM; when it reaches a point that needs
// judgement it queues a Decision (see core/decision.go). `decisions` shows what
// is waiting and lets the operator answer it. Resolving is a real act: the
// command prints what actually changed in the world, not a confirmation.
func init() {
	builtinTable["decisions"] = cmdDecisions
}

func cmdDecisions(s *Shell, args []string) int {
	if len(args) == 0 || args[0] == "list" {
		return decisionsList(s, false)
	}
	switch args[0] {
	case "all", "history", "log":
		return decisionsList(s, true)
	case "resolve", "decide":
		if len(args) < 3 {
			s.errf("usage: decisions resolve <id> <action>")
			return 1
		}
		id, action := args[1], args[2]
		if err := s.W.ResolveDecision(id, action, s.User.Name); err != nil {
			s.errf("decisions: %v", err)
			return 1
		}
		d := decisionByID(s.W, id)
		if d != nil {
			fmt.Fprintf(s.Out, "%s resolved (%s): %s\n", d.ID, d.Action, d.Result)
		}
		return 0
	}
	s.errf("usage: decisions [list|all|resolve <id> <action>]")
	return 1
}

// decisionsList prints the queue: kind, subject, how many ticks it has been
// waiting and the detail the rule layer recorded. `all` also shows answers.
func decisionsList(s *Shell, all bool) int {
	var rows []*core.Decision
	var pending int
	for _, d := range s.W.Decisions {
		if d.Status == core.DecisionPending {
			pending++
		}
		if all || d.Status == core.DecisionPending {
			rows = append(rows, d)
		}
	}
	if len(rows) == 0 {
		if all {
			fmt.Fprintln(s.Out, "no decisions queued yet")
		} else {
			fmt.Fprintln(s.Out, "no pending decisions — the rule layer is running on its own")
		}
		return 0
	}
	if !all && pending == 0 {
		fmt.Fprintln(s.Out, "no pending decisions — the rule layer is running on its own")
		return 0
	}
	fmt.Fprintf(s.Out, "%-8s %-16s %-24s %-6s %s\n", "ID", "KIND", "SUBJECT", "AGE", "DETAIL")
	for _, d := range rows {
		age := s.W.TickCount - d.CreatedTick
		fmt.Fprintf(s.Out, "%-8s %-16s %-24s %-6s %s\n", d.ID, d.Kind, clip(d.Subject, 24), fmt.Sprintf("%dt", age), d.Detail)
		if all && d.Status != core.DecisionPending {
			by := d.ResolvedBy
			if by == "" {
				by = "world"
			}
			fmt.Fprintf(s.Out, "         -> %s by %s: %s\n", d.Status, by, d.Result)
		}
	}
	if pending > 0 {
		fmt.Fprintln(s.Out, "\nresolve with: decisions resolve <id> <action>")
	}
	return 0
}

func decisionByID(w *core.World, id string) *core.Decision {
	for _, d := range w.Decisions {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
