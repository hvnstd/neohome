package shell

// Package managers (§9 §10 §11).
//
// One engine, five distributions: the manager that actually exists on the box
// reads that box's own sources configuration, fetches the metadata its family
// publishes, verifies it the way that family verifies it, caches the indexes
// locally, and installs the payload the repository serves. Installing without
// updating first fails for the same reason it does on a real box: there is
// nothing cached to install from.

import (
	"fmt"
	"sort"
	"strings"

	"neohome/internal/core"
)

func init() {
	for _, e := range []struct {
		name string
		fn   Cmd
	}{
		{"apt", cmdApt}, {"apk", cmdApk}, {"pacman", cmdPacman},
		{"dnf", cmdDnf}, {"opkg", cmdOpkg}, {"mirror-sync", cmdMirrorSync},
		{"pkg", cmdPkg},
	} {
		builtinTable[e.name] = e.fn
	}
}

// ---- community packages: export installed software, import payload files ----

// pkg export/import move software as files: an installed package renders to
// the community payload format (shareable over dead drops, the bazaar, or
// plain scp), and importing parses and installs it through the same apply
// path as repository software — unsigned, dependency-checked, logged.
func cmdPkg(s *Shell, args []string) int {
	if len(args) == 0 {
		s.errf("usage: pkg export NAME [FILE] | pkg import FILE")
		return 1
	}
	switch args[0] {
	case "export":
		return pkgExport(s, args[1:])
	case "import":
		return pkgImport(s, args[1:])
	}
	s.errf("usage: pkg export NAME [FILE] | pkg import FILE")
	return 1
}

func pkgExport(s *Shell, args []string) int {
	if len(args) < 1 || len(args) > 2 {
		s.errf("usage: pkg export NAME [FILE]")
		return 1
	}
	p := s.Dev.Installed[args[0]]
	if p == nil {
		s.errf("pkg: %s is not installed here", args[0])
		return 1
	}
	r := &core.Repo{Distro: distroKey(s.Dev)}
	if src, ok := s.Dev.InstalledFrom[args[0]]; ok {
		if repo := s.W.Repos[src]; repo != nil {
			r = repo
		}
	}
	body := core.RenderPayload(r, p)
	dst := args[0] + ".npkg"
	if len(args) == 2 {
		dst = args[1]
	}
	if err := s.Dev.WriteGuest(s.abs(dst), []byte(body), s.User); err != nil {
		s.errf("pkg: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "exported %s %s to %s (%d bytes, shareable)\n", p.Name, p.Version, dst, len(body))
	return 0
}

func pkgImport(s *Shell, args []string) int {
	if len(args) != 1 {
		s.errf("usage: pkg import FILE")
		return 1
	}
	if s.User.UID != 0 {
		s.errf("pkg: importing software needs root")
		return 1
	}
	data, exists, allowed := s.Dev.FS.ReadPathAs(s.abs(args[0]), s.User)
	if !exists {
		s.errf("pkg: %s: No such file", args[0])
		return 1
	}
	if !allowed {
		s.errf("pkg: %s: Permission denied", args[0])
		return 1
	}
	p, err := core.ParsePayload(string(data))
	if err != nil {
		s.errf("pkg: %s: not a package (%v)", args[0], err)
		return 1
	}
	actions, err := s.W.InstallCommunity(s.Dev, s.User, p, args[0])
	if err != nil {
		if perr, ok := err.(*core.PackageError); ok {
			mgrErr(s, core.ManagerFor(s.Dev), perr)
			return 1
		}
		s.errf("pkg: %v", err)
		return 1
	}
	for _, a := range actions {
		fmt.Fprintf(s.Out, "%s\n", a)
	}
	return 0
}

// distroKey maps a device to its repository family for rendering.
func distroKey(d *core.Device) string {
	if m := core.ManagerFor(d); m != "" {
		switch m {
		case "apt":
			return "debian"
		case "apk":
			return "alpine"
		case "pacman":
			return "arch"
		case "dnf":
			return "fedora"
		case "opkg":
			return "openwrt"
		}
	}
	return "debian"
}

// ---- manager voice ----

type mgrStyle struct {
	// errPrefix is how this manager reports a failure.
	errPrefix string
	// warnPrefix is how it reports something true but concerning.
	warnPrefix string
	// notRoot is the honest refusal a non-root user gets.
	notRoot []string
}

var mgrStyles = map[string]mgrStyle{
	"apt": {errPrefix: "E: ", warnPrefix: "W: ", notRoot: []string{
		"E: Could not open lock file /var/lib/dpkg/lock-frontend - open (13: Permission denied)",
		"E: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), are you root?"}},
	"apk": {errPrefix: "ERROR: ", warnPrefix: "WARNING: ", notRoot: []string{
		"ERROR: Unable to lock database: Permission denied",
		"ERROR: Failed to open apk database: Permission denied"}},
	"pacman": {errPrefix: "error: ", warnPrefix: "warning: ", notRoot: []string{
		"error: you cannot perform this operation unless you are root."}},
	"dnf": {errPrefix: "Error: ", warnPrefix: "Warning: ", notRoot: []string{
		"Error: This command has to be run with superuser privileges (under the root user on most systems)."}},
	"opkg": {errPrefix: " * ", warnPrefix: " * ", notRoot: []string{
		"Collected errors:", " * opkg_conf_load: Could not create lock file /var/lock/opkg.lock: Permission denied."}},
}

// managerGate enforces that the command belongs to this box and that the
// operation needs root, exactly as the distribution would.
func managerGate(s *Shell, mgr string, needRoot bool) bool {
	if !core.HasManager(s.Dev, mgr) {
		fmt.Fprintf(s.Out, "%s: %s: command not found\r\n", s.Dev.Hostname, mgr)
		return false
	}
	if needRoot && s.User.UID != 0 {
		for _, line := range mgrStyles[mgr].notRoot {
			fmt.Fprintf(s.Out, "%s\n", line)
		}
		return false
	}
	return true
}

func mgrErr(s *Shell, mgr string, err *core.PackageError) {
	st := mgrStyles[mgr]
	if st.errPrefix == " * " {
		fmt.Fprintf(s.Out, "Collected errors:\n * %s\n", err.Msg)
	} else {
		fmt.Fprintf(s.Out, "%s%s\n", st.errPrefix, err.Msg)
	}
	if err.Hint != "" && mgr != "opkg" {
		fmt.Fprintf(s.Out, "%s%s\n", st.warnPrefix, err.Hint)
	}
}

func mgrPrintWarnings(s *Shell, mgr string, view *core.RepoView) {
	for _, w := range view.Warnings {
		st := mgrStyles[mgr]
		prefix := st.warnPrefix
		if mgr == "opkg" {
			prefix = ""
		}
		fmt.Fprintf(s.Out, "%s%s\n", prefix, w)
	}
}

// sources returns the device's configured sources, reporting the ones no
// repository serves — a misconfiguration the player can see and fix.
func sources(s *Shell, mgr string) []core.Source {
	all := s.W.SourcesForDevice(s.Dev)
	var good []core.Source
	for _, src := range all {
		if src.Repo == nil {
			if mgr == "apt" {
				fmt.Fprintf(s.Out, "N: Ignoring file %q as it has no mirror in this world: %s\n", src.File, src.Line)
			}
			continue
		}
		good = append(good, src)
	}
	return good
}

// ---- update ----

func updateRepos(s *Shell, mgr string) int {
	srcs := sources(s, mgr)
	if len(srcs) == 0 {
		fmt.Fprintf(s.Out, "%sno repositories are configured on %s (see %s)\r\n",
			mgrStyles[mgr].errPrefix, s.Dev.Hostname, strings.Join(core.DistroFor(s.Dev).Sources, ", "))
		return 1
	}
	failed := 0
	// Get: numbers count up across every source, the way the real fetcher
	// reports progress through the whole run, not per repository
	gets := 0
	for _, src := range srcs {
		view, err := s.W.UpdateRepo(s.Dev, src)
		if err != nil {
			mgrErr(s, mgr, err)
			failed++
			continue
		}
		switch mgr {
		case "apt":
			gets++
			fmt.Fprintf(s.Out, "Get:%d http://%s/%s %s InRelease\n", gets, view.Repo.URL, core.ReleaseRel(view.Repo), view.Repo.Suite)
			for _, comp := range view.Comps {
				gets++
				fmt.Fprintf(s.Out, "Get:%d http://%s/%s %s Packages\n", gets, view.Repo.URL, core.IndexRel(view.Repo, comp), src.Suite)
			}
		case "apk":
			fmt.Fprintf(s.Out, "fetch http://%s/%s\n", view.Repo.URL, core.ReleaseRel(view.Repo))
		case "pacman":
			fmt.Fprintf(s.Out, ":: Synchronizing package databases...\n core is up to date\n")
		case "dnf":
			fmt.Fprintf(s.Out, "NeoHome Packages                                          %6d  B/s | %6d  B    00:01\n",
				len(view.Entries)*120, len(view.Entries)*120)
			fmt.Fprintf(s.Out, "Dependencies resolved.\nNothing to do.\nComplete!\n")
		case "opkg":
			fmt.Fprintf(s.Out, "Downloading http://%s/%s\n", view.Repo.URL, core.ReleaseRel(view.Repo))
		}
		mgrPrintWarnings(s, mgr, view)
		if mgr == "apt" {
			fmt.Fprintf(s.Out, "Reading package lists... Done\n")
		}
	}
	// A source that failed is a source the box cannot install from. Real apt
	// exits non-zero if any index could not be fetched, and so does this: a
	// script that chains `update && install` must not sail past a bad source.
	s.Dev.Logf("info", "pkg", "%s update: %d source(s), %d failed", mgr, len(srcs), failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// ---- views ----

// cachedViews loads what the box can currently install from. A box that never
// updated has nothing, and says so.
func cachedViews(s *Shell, mgr string) []*core.RepoView {
	var views []*core.RepoView
	hadSource := false
	for _, src := range sources(s, mgr) {
		hadSource = true
		view, err := s.W.LoadCachedRepo(s.Dev, src)
		if err != nil {
			continue
		}
		views = append(views, view)
	}
	if len(views) == 0 && hadSource {
		fmt.Fprintf(s.Out, "%sno package lists are cached on this system — run '%s update' first\r\n",
			mgrStyles[mgr].errPrefix, mgr)
	}
	return views
}

// ---- install ----

func installPackages(s *Shell, mgr string, names []string) int {
	if len(names) == 0 {
		mgrErr(s, mgr, &core.PackageError{Msg: "no packages requested"})
		return 1
	}
	views := cachedViews(s, mgr)
	if len(views) == 0 {
		return 1
	}
	plan, err := core.PlanInstall(s.Dev, views, names)
	if err != nil {
		if err.Code == "MISSING_DEP" {
			mgrErr(s, mgr, &core.PackageError{Code: err.Code,
				Msg:  err.Msg,
				Hint: "the dependency is not in any repository this box sees"})
			if mgr == "apt" {
				fmt.Fprintln(s.Out, "E: Unable to correct problems, you have held broken packages.")
			}
			return 1
		}
		mgrErr(s, mgr, err)
		return 1
	}
	var todo []string
	size := 0
	for _, step := range plan {
		if s.Dev.Installed[step.Entry.Name] != nil {
			continue
		}
		todo = append(todo, step.Entry.Name+" ("+step.Entry.Version+")")
		size += step.Entry.Size
	}
	if len(todo) == 0 {
		fmt.Fprintf(s.Out, "already up to date: %s\n", strings.Join(names, " "))
		return 0
	}
	switch mgr {
	case "apt":
		fmt.Fprintf(s.Out, "Reading package lists... Done\nBuilding dependency tree... Done\nThe following NEW packages will be installed:\n  %s\n",
			strings.Join(todo, " "))
		fmt.Fprintf(s.Out, "0 upgraded, %d newly installed, 0 to remove and 0 not upgraded.\n", len(todo))
		fmt.Fprintf(s.Out, "Need to get %d kB of archives.\nAfter this operation, %d kB of additional disk space will be used.\n", size, size)
	case "apk":
		fmt.Fprintf(s.Out, "(1/%d) Installing %s\n", len(todo), strings.Join(todo, " "))
	case "pacman":
		fmt.Fprintf(s.Out, "resolving dependencies...\nlooking for conflicting packages...\n\nPackages (%d) %s\n\nTotal Download Size:   %d.%02d MiB\n",
			len(todo), strings.Join(todo, "  "), size/1024, size%1024)
	case "dnf":
		fmt.Fprintf(s.Out, "Dependencies resolved.\n================================================================================\n Package        Architecture   Version        Repository      Size\n================================================================================\n Installing:\n")
		for _, step := range plan {
			if s.Dev.Installed[step.Entry.Name] != nil {
				continue
			}
			fmt.Fprintf(s.Out, " %-14s %-14s %-14s %-15s %d k\n",
				step.Entry.Name, step.Entry.Arch, step.Entry.Version, step.View.Repo.Name, step.Entry.Size)
		}
		fmt.Fprintf(s.Out, "================================================================================\n\nTotal download size: %d k\n", size)
	case "opkg":
		fmt.Fprintf(s.Out, "Installing %s...\n", strings.Join(todo, " "))
	}
	done, ierr := s.W.InstallFromView(s.Dev, plan)
	for _, pkg := range done {
		if mgr == "apt" {
			fmt.Fprintf(s.Out, "Selecting previously unselected package %s.\n", pkg.Name)
			fmt.Fprintf(s.Out, "(Reading database ... 1 files and directories currently installed.)\n")
			fmt.Fprintf(s.Out, "Preparing to unpack .../%s_%s.vpkg ...\n", pkg.Name, pkg.Version)
			fmt.Fprintf(s.Out, "Unpacking %s (%s) ...\n", pkg.Name, pkg.Version)
			fmt.Fprintf(s.Out, "Setting up %s (%s) ...\n", pkg.Name, pkg.Version)
			for _, a := range pkg.Actions {
				fmt.Fprintf(s.Out, "  %s\n", a)
			}
			continue
		}
		fmt.Fprintf(s.Out, " * %s %s\n", pkg.Name, pkg.Version)
		for _, a := range pkg.Actions {
			fmt.Fprintf(s.Out, " * %s\n", a)
		}
	}
	if ierr != nil {
		mgrErr(s, mgr, ierr)
		return 1
	}
	fmt.Fprintf(s.Out, "%s\n", map[string]string{
		"apt":    "Processing triggers for man-db (2.12.0-4) ...",
		"apk":    "OK: " + fmt.Sprint(len(todo)) + " package(s) installed",
		"pacman": ":: Running post-transaction hooks...\n(1/1) Arming ConditionNeedsUpdate...",
		"dnf":    "Complete!",
		"opkg":   "Configuring " + strings.Join(todo, " ") + ".",
	}[mgr])
	return 0
}

// ---- remove ----

func removePackages(s *Shell, mgr string, names []string) int {
	if len(names) == 0 {
		mgrErr(s, mgr, &core.PackageError{Msg: "no packages given"})
		return 1
	}
	rc := 0
	for _, name := range names {
		p := s.Dev.Installed[name]
		if p == nil {
			mgrErr(s, mgr, &core.PackageError{Code: "NOT_INSTALLED", Msg: "package " + name + " is not installed"})
			rc = 1
			continue
		}
		if err := core.RemoveCheck(s.Dev, name); err != nil {
			mgrErr(s, mgr, err)
			rc = 1
			continue
		}
		fmt.Fprintf(s.Out, "Removing %s (%s) ...\n", p.Name, p.Version)
		for _, a := range s.W.RemovePkg(s.Dev, p) {
			fmt.Fprintf(s.Out, " * %s\n", a)
		}
	}
	return rc
}

// ---- search / list ----

func listAvailable(s *Shell, mgr string, installedOnly bool, term string) int {
	if installedOnly {
		return listInstalled(s, mgr, term)
	}
	views := cachedViews(s, mgr)
	if len(views) == 0 {
		return 1
	}
	seen := map[string]bool{}
	for _, v := range views {
		entries := append([]core.IndexEntry(nil), v.Entries...)
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		for _, e := range entries {
			if seen[e.Name] {
				continue
			}
			have := s.Dev.Installed[e.Name] != nil
			if term != "" && !strings.Contains(strings.ToLower(e.Name+" "+e.Desc), strings.ToLower(term)) {
				continue
			}
			seen[e.Name] = true
			switch mgr {
			case "apt":
				suffix := ""
				if have {
					suffix = " [installed]"
				}
				fmt.Fprintf(s.Out, "%s/%s %s %s%s\n", e.Name, v.Repo.Suite, e.Version, e.Arch, suffix)
			case "apk":
				fmt.Fprintf(s.Out, "%s-%s %s{%s}\n", e.Name, e.Version, e.Desc, v.Repo.Name)
			case "pacman":
				fmt.Fprintf(s.Out, "core/%s %s %s%s\n", e.Name, e.Version, e.Desc,
					map[bool]string{true: " [installed]", false: ""}[have])
			case "dnf":
				fmt.Fprintf(s.Out, "%s.%s   %s   neohome\n", e.Name, e.Arch, e.Version)
			case "opkg":
				fmt.Fprintf(s.Out, "%s - %s (%s)\n", e.Name, e.Version, e.Desc)
			}
		}
	}
	return 0
}

// isInstalledFlag accepts the spellings each manager's users actually type for
// "show me what is installed".
func isInstalledFlag(a string) bool {
	switch a {
	case "installed", "--installed", "-i", "--installed-only":
		return true
	}
	return false
}

// listInstalled reports what is really on this box, from its own record of
// installed packages — no repositories, no cached lists, no network.
func listInstalled(s *Shell, mgr string, term string) int {
	names := make([]string, 0, len(s.Dev.Installed))
	for n := range s.Dev.Installed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := s.Dev.Installed[n]
		if term != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Desc), strings.ToLower(term)) {
			continue
		}
		repo := s.W.RepoForDevice(s.Dev, p.Name)
		origin := s.Dev.InstalledFrom[p.Name]
		if origin == "" && repo != nil {
			origin = repo.Name
		}
		if repo == nil {
			repo = s.W.RepoOf(p.Name)
		}
		arch := p.Arch
		if arch == "" && repo != nil {
			arch = repo.Arch()
		}
		switch mgr {
		case "apt":
			suite := ""
			if repo != nil {
				suite = repo.Suite
			}
			line := fmt.Sprintf("%s/%s %s %s [installed]", p.Name, suite, p.Version, arch)
			if origin != "" {
				line += ",origin=" + origin
			}
			fmt.Fprintln(s.Out, line)
		case "apk":
			fmt.Fprintf(s.Out, "%s-%s %s\n", p.Name, p.Version, p.Desc)
		case "pacman":
			fmt.Fprintf(s.Out, "core/%s %s %s [installed]\n", p.Name, p.Version, p.Desc)
		case "dnf":
			if origin == "" {
				origin = "system"
			}
			fmt.Fprintf(s.Out, "%s.%s   %s   @%s\n", p.Name, arch, p.Version, origin)
		case "opkg":
			fmt.Fprintf(s.Out, "%s - %s (%s)\n", p.Name, p.Version, p.Desc)
		}
	}
	return 0
}

// ---- the commands ----

func cmdApt(s *Shell, args []string) int {
	if !managerGate(s, "apt", false) {
		return 1
	}
	if len(args) == 0 {
		s.errf("apt: usage: apt [update|install|remove|search|list]")
		return 1
	}
	switch args[0] {
	case "update":
		if !managerGate(s, "apt", true) {
			return 1
		}
		return updateRepos(s, "apt")
	case "install", "upgrade":
		if !managerGate(s, "apt", true) {
			return 1
		}
		if args[0] == "upgrade" {
			fmt.Fprintln(s.Out, "Reading package lists... Done\nCalculating upgrade... Done\n0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.")
			return 0
		}
		return installPackages(s, "apt", args[1:])
	case "remove", "purge":
		if !managerGate(s, "apt", true) {
			return 1
		}
		return removePackages(s, "apt", args[1:])
	case "search":
		return listAvailable(s, "apt", false, strings.Join(args[1:], " "))
	case "list":
		return listAvailable(s, "apt", len(args) > 1 && isInstalledFlag(args[1]), "")
	}
	fmt.Fprintf(s.Out, "E: Invalid operation %s\n", args[0])
	return 1
}

func cmdApk(s *Shell, args []string) int {
	if !managerGate(s, "apk", false) {
		return 1
	}
	if len(args) == 0 {
		s.errf("apk: usage: apk [update|add|del|search|list]")
		return 1
	}
	switch args[0] {
	case "update", "-U":
		if !managerGate(s, "apk", true) {
			return 1
		}
		return updateRepos(s, "apk")
	case "add":
		if !managerGate(s, "apk", true) {
			return 1
		}
		return installPackages(s, "apk", args[1:])
	case "del", "remove":
		if !managerGate(s, "apk", true) {
			return 1
		}
		return removePackages(s, "apk", args[1:])
	case "search":
		return listAvailable(s, "apk", false, strings.Join(args[1:], " "))
	case "list", "info":
		return listAvailable(s, "apk", len(args) > 1 && args[1] == "-I", "")
	}
	fmt.Fprintf(s.Out, "apk: unrecognized command '%s'\n", args[0])
	return 1
}

func cmdPacman(s *Shell, args []string) int {
	if !managerGate(s, "pacman", false) {
		return 1
	}
	if len(args) == 0 {
		s.errf("pacman: usage: pacman -Sy | -S PKG | -R PKG | -Ss TERM | -Q")
		return 1
	}
	flag := args[0]
	switch {
	case strings.HasPrefix(flag, "-Sy"), strings.HasPrefix(flag, "-Syu"), strings.HasPrefix(flag, "-Syuu"), strings.HasPrefix(flag, "-Syy"):
		if !managerGate(s, "pacman", true) {
			return 1
		}
		if len(args) > 1 {
			return installPackages(s, "pacman", args[1:])
		}
		return updateRepos(s, "pacman")
	case strings.HasPrefix(flag, "-S"):
		if !managerGate(s, "pacman", true) {
			return 1
		}
		if len(args) > 1 && (args[1] == "-s" || args[1] == "--search") {
			return listAvailable(s, "pacman", false, strings.Join(args[2:], " "))
		}
		return installPackages(s, "pacman", args[1:])
	case strings.HasPrefix(flag, "-R"):
		if !managerGate(s, "pacman", true) {
			return 1
		}
		return removePackages(s, "pacman", args[1:])
	case strings.HasPrefix(flag, "-Ss"):
		return listAvailable(s, "pacman", false, strings.Join(append(strings.Split(strings.TrimPrefix(flag, "-Ss"), " "), args[1:]...), " "))
	case strings.HasPrefix(flag, "-Q"):
		return listAvailable(s, "pacman", true, "")
	}
	fmt.Fprintf(s.Out, "error: invalid option '%s'\n", flag)
	return 1
}

func cmdDnf(s *Shell, args []string) int {
	if !managerGate(s, "dnf", false) {
		return 1
	}
	if len(args) == 0 {
		s.errf("dnf: usage: dnf [install|remove|check-update|search|list]")
		return 1
	}
	switch args[0] {
	case "check-update", "update", "upgrade", "makecache":
		if !managerGate(s, "dnf", true) {
			return 1
		}
		return updateRepos(s, "dnf")
	case "install", "reinstall":
		if !managerGate(s, "dnf", true) {
			return 1
		}
		return installPackages(s, "dnf", args[1:])
	case "remove", "erase":
		if !managerGate(s, "dnf", true) {
			return 1
		}
		return removePackages(s, "dnf", args[1:])
	case "search":
		return listAvailable(s, "dnf", false, strings.Join(args[1:], " "))
	case "list":
		return listAvailable(s, "dnf", len(args) > 1 && isInstalledFlag(args[1]), "")
	}
	fmt.Fprintf(s.Out, "Error: Unknown command '%s'\n", args[0])
	return 1
}

func cmdOpkg(s *Shell, args []string) int {
	if !managerGate(s, "opkg", false) {
		return 1
	}
	if len(args) == 0 {
		s.errf("opkg: usage: opkg [update|install|remove|list]")
		return 1
	}
	switch args[0] {
	case "update":
		if !managerGate(s, "opkg", true) {
			return 1
		}
		return updateRepos(s, "opkg")
	case "install":
		if !managerGate(s, "opkg", true) {
			return 1
		}
		return installPackages(s, "opkg", args[1:])
	case "remove", "uninstall":
		if !managerGate(s, "opkg", true) {
			return 1
		}
		return removePackages(s, "opkg", args[1:])
	case "list", "list-installed", "info":
		inst := args[0] == "list-installed" || (len(args) > 1 && isInstalledFlag(args[1]))
		return listAvailable(s, "opkg", inst, strings.Join(args[1:], " "))
	}
	fmt.Fprintf(s.Out, "opkg: unrecognized command '%s'\n", args[0])
	return 1
}

// ---- the mirror's own tool ----

// cmdMirrorSync is the command the mirror host's cron lines run and the one a
// player can run by hand after fixing the schedule. It is a real operation on
// real bytes, not a nudge to a background job.
func cmdMirrorSync(s *Shell, args []string) int {
	if len(args) >= 1 && args[0] == "build" {
		if _, rc := cmdMirrorBuild(s, args[1:]); rc != 0 {
			return rc
		}
		return 0
	}
	if s.User.UID != 0 {
		s.errf("mirror-sync: must be run as root")
		return 1
	}
	names := make([]string, 0, len(s.W.Repos))
	for n, r := range s.W.Repos {
		if r.DeviceID == s.Dev.ID && r.IsMirrored() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Fprintf(s.Out, "%s does not serve any repository\n", s.Dev.Hostname)
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintf(s.Out, "repositories served by %s:\n", s.Dev.Hostname)
		fmt.Fprintf(s.Out, "%-10s %-8s %-10s %-26s %s\n", "REPO", "SUITE", "STATUS", "LAST SYNC", "COMPONENTS")
		for _, n := range names {
			r := s.W.Repos[n]
			age := "never"
			if !r.LastSync.IsZero() {
				age = core.HumanAge(s.W.Sim.Sub(r.LastSync)) + " ago"
			}
			fmt.Fprintf(s.Out, "%-10s %-8s %-10s %-26s %s\n", r.Name, r.Suite, r.Status, age, strings.Join(r.Comps, " "))
			if r.StatusWhy != "" {
				fmt.Fprintf(s.Out, "    why: %s\n", r.StatusWhy)
			}
		}
		fmt.Fprintf(s.Out, "\nusage: mirror-sync all | mirror-sync %s\n", strings.Join(names, "|"))
		return 0
	}
	targets := args
	if len(args) == 1 && args[0] == "all" {
		targets = names
	}
	rc := 0
	for _, t := range targets {
		r := s.W.Repos[t]
		if r == nil || r.DeviceID != s.Dev.ID {
			fmt.Fprintf(s.Out, "mirror-sync: %s is not served by this host\n", t)
			rc = 1
			continue
		}
		if err := s.W.StartMirrorSync(r); err != nil {
			fmt.Fprintf(s.Out, "mirror-sync: %s\n", err.Msg)
			rc = 1
			continue
		}
		fmt.Fprintf(s.Out, "sync started for %s (suite %s) — it runs as a real process; `ps` shows it, `logread` records the result\n", r.Name, r.Suite)
	}
	return rc
}
