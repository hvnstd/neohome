package core

import (
	"fmt"
	"time"
)

// ReplState is MariaDB master/slave replication state (§52). The shape is
// owned by this file.
//
// One struct carries both halves because in this world a machine can be a
// master of its own binlog and a slave of somebody else's at the same time,
// exactly as a real server can. The master half is the binlog: a file name, a
// monotonically increasing position and the events that got us there. The
// slave half is the connection to one upstream plus the last position that
// machine has really applied.
//
// The binlog stores *what changed*, not the bytes: an event names the database
// and table whose real files are the object of the change. Applying an event
// copies those files from the master's VFS through the slave's own write gate,
// so a full disk, a failing disk or an audit rule are the same facts they are
// for any other write. That keeps the model honest and small: the log is a
// pointer, the files are the truth.
type ReplState struct {
	// ---- master side ----
	BinLogFile string        // e.g. "mysql-bin.000001"
	BinLogPos  int           // the last assigned position
	BinLog     []BinLogEvent // every event, in position order

	// ---- slave side ----
	MasterHost     string
	MasterUser     string
	MasterPassword string
	MasterLogFile  string
	MasterLogPos   int       // the master position this slave has applied
	Running        bool      // whether START SLAVE set the apply thread running
	LastError      string    // why the last apply attempt did not make progress
	LastSync       time.Time // world clock of the last applied event
	Lag            int       // unapplied events behind the master
}

// BinLogEvent is one row-level or DDL change. Pos is its binlog position (also
// its 1-based ordinal); DB and Table name the object whose files carry the
// change; Op is one of CREATE DATABASE, DROP DATABASE, CREATE TABLE,
// DROP TABLE, INSERT, UPDATE, DELETE.
type BinLogEvent struct {
	Pos   int
	DB    string
	Table string
	Op    string
}

// replProcFor finds the apply thread on a slave. It is a real process in the
// device's table, so killing it (which a player does with `kill`/`pkill`)
// really stops replication instead of a flag deciding whether work happens.
func replProcFor(d *Device) *Proc {
	for _, p := range d.Procs {
		if p.Name == "mariadb-repl" {
			return p
		}
	}
	return nil
}

// startReplProc adds the ps-visible apply thread, idempotently.
func (w *World) startReplProc(d *Device) {
	if replProcFor(d) != nil {
		return
	}
	// The thread belongs to the database server. A system `mysql` account is
	// the natural owner where the package made one; otherwise root runs it.
	who := "root"
	if d.FindUser("mysql") != nil {
		who = "mysql"
	}
	d.AddProc(&Proc{Name: "mariadb-repl", User: who, CPU: 2, WantCPU: 2, Mem: 32, TTY: "?",
		State: "S", Start: w.Sim, StartTick: w.TickCount, Kind: "builtin"})
}

// stopReplProc removes the apply thread; the operator's RUNNING flag is
// separate state and is not touched here (STOP SLAVE clears it itself).
func (w *World) stopReplProc(d *Device) {
	for i := 0; i < len(d.Procs); i++ {
		if d.Procs[i].Name == "mariadb-repl" {
			d.Procs = append(d.Procs[:i], d.Procs[i+1:]...)
			return
		}
	}
}

// ReplTick advances one binlog event per running slave per tick. Wired from
// World.Tick after BCTick. Every step goes through the same network and disk
// gates as a player's own command: DNS is resolved fresh, the master is dialed
// (power/link/firewall/ban all vote), the configured account proves its
// password, and the files land through WriteGuest.
func (w *World) ReplTick() {
	for _, d := range w.Devices {
		r := d.Repl
		if r == nil || !r.Running {
			continue
		}
		// A dark machine runs no jobs. Running stays as the operator set it;
		// coming back up resumes where the proc exists again.
		if !d.Powered() {
			continue
		}
		// A killed apply thread stops progress even though RUNNING is still
		// set — the process *is* the work, not a decoration of it.
		if replProcFor(d) == nil {
			continue
		}
		w.replStep(d, r)
	}
}

// replStep is one slave's attempt to apply its next event.
func (w *World) replStep(slave *Device, r *ReplState) {
	if r.MasterHost == "" {
		r.LastError = "START SLAVE: no MASTER_HOST configured (run CHANGE MASTER TO first)"
		return
	}
	// Resolve every tick: a name that moved, or a resolver that broke, is a
	// fact about now, never a cached address.
	ip, ok, why := DNSAnswer(slave, r.MasterHost)
	if !ok {
		r.LastError = "resolve " + r.MasterHost + ": " + why
		return
	}
	svc, master, msg := Dial(slave, ip, 3306)
	if svc == nil || master == nil {
		r.LastError = msg
		return
	}
	// The credential is the master device's own account, the same rule ssh,
	// sftp and imap enforce. A rejection is recorded by the master.
	mu := master.FindUser(r.MasterUser)
	if !mu.CheckPassword(r.MasterPassword) {
		master.NoteAuthFail(slave.SourceIPFor(master), slave.Hostname, "mysql replication password for "+r.MasterUser)
		r.LastError = fmt.Sprintf("Access denied for user '%s'@'%s'", r.MasterUser, r.MasterHost)
		return
	}
	r.LastError = ""

	mr := master.Repl
	if mr == nil || r.MasterLogPos >= mr.BinLogPos {
		r.Lag = 0
		return
	}
	ev, ok := replEventAt(mr, r.MasterLogPos+1)
	if !ok {
		r.LastError = fmt.Sprintf("binlog event at position %d is missing on %s", r.MasterLogPos+1, master.Hostname)
		return
	}
	if err := w.replApply(slave, master, ev); err != nil {
		r.LastError = err.Error()
		return
	}
	r.MasterLogPos = ev.Pos
	r.MasterLogFile = mr.BinLogFile
	r.LastSync = w.Sim
	r.Lag = mr.BinLogPos - r.MasterLogPos
	if r.Lag < 0 {
		r.Lag = 0
	}
}

// replEventAt returns the event at an exact binlog position.
func replEventAt(r *ReplState, pos int) (BinLogEvent, bool) {
	for _, e := range r.BinLog {
		if e.Pos == pos {
			return e, true
		}
	}
	return BinLogEvent{}, false
}

// replApply performs one event on the slave. Table changes copy the object's
// real files from the master's VFS; drops and database creation are the real
// filesystem operations they always were.
func (w *World) replApply(slave, master *Device, ev BinLogEvent) error {
	switch ev.Op {
	case "CREATE DATABASE":
		return replMkdir(slave, master, ev.DB)
	case "DROP DATABASE":
		dir, err := mysqlDBDir(ev.DB)
		if err != nil {
			return err
		}
		slave.FS.Remove(dir)
		return nil
	case "DROP TABLE":
		dir, err := mysqlDBDir(ev.DB)
		if err != nil {
			return err
		}
		slave.FS.Remove(dir + "/" + ev.Table + ".schema")
		slave.FS.Remove(dir + "/" + ev.Table + ".rows")
		return nil
	case "CREATE TABLE", "INSERT", "UPDATE", "DELETE":
		return replCopyTable(slave, master, ev.DB, ev.Table)
	}
	return nil
}

// replMkdir creates the database directory on the slave, keeping the master's
// owner so a later `mysql` session on the slave judges access the same way.
func replMkdir(slave, master *Device, db string) error {
	dir, err := mysqlDBDir(db)
	if err != nil {
		return err
	}
	owner := "root"
	if n, ok := master.FS.Get(dir); ok && slave.FindUser(n.Owner) != nil {
		owner = n.Owner
	}
	slave.FS.MkdirAll(dir, 0755, owner, owner)
	return nil
}

// replCopyTable copies a table's schema and rows from the master to the slave
// through the slave's single write gate, so ENOSPC, EIO and auditd all apply.
// A file the master no longer has is removed here too, which is how a dropped
// row set stays dropped.
func replCopyTable(slave, master *Device, db, table string) error {
	dir, err := mysqlDBDir(db)
	if err != nil {
		return err
	}
	if err := replMkdir(slave, master, db); err != nil {
		return err
	}
	for _, ext := range []string{".schema", ".rows"} {
		src := dir + "/" + table + ext
		data, ok := master.FS.Read(src)
		if !ok {
			slave.FS.Remove(src)
			continue
		}
		u := replWriter(slave, master, src)
		if u == nil {
			return fmt.Errorf("replication: no account on %s can apply %s", slave.Hostname, src)
		}
		if err := slave.WriteGuest(src, data, u); err != nil {
			return fmt.Errorf("replication: apply %s: %v", src, err)
		}
	}
	return nil
}

// replWriter chooses the account a copied file is written as: the slave's
// namesake of the master file's owner where that account exists (so ownership,
// and therefore access, survives), root otherwise.
func replWriter(slave, master *Device, path string) *User {
	if n, ok := master.FS.Get(path); ok {
		if u := slave.FindUser(n.Owner); u != nil {
			return u
		}
	}
	return slave.FindUser("root")
}
