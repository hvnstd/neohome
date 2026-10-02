#!/usr/bin/env bash
set -eu
cd /workspace/neohome
export PATH=/workspace/go/bin:$PATH GOCACHE=/workspace/gocache GOPATH=/workspace/gopath
go build ./... && go vet ./... && go test ./tests/ -count=1 | tail -1
git -c user.name="Neko" -c user.email="neko@neohome.local" add -A
git -c user.name="Neko" -c user.email="neko@neohome.local" commit -q -m "tmux: parse -t like tmux does, and prove the session loop live

\`tmux send -t <session>\` / \`attach -t\` / \`kill-session -t\` were treating
\"-t\" as the session name and then reporting \"session not found: -t\", so
sending into a detached session silently did nothing. One tmuxTarget() parser
now handles \"-t name\", \"-tname\" and a bare positional name.

tools/tmux_verify.sh drives it over real ssh: no sessions (previously a panic),
create -> a live process appears in ps, send -> attach shows the session's own
output with a (tmux:name) prompt, kill -> the process is gone."
git log --oneline | head -1
