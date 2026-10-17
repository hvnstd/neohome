package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// §52 MariaDB master/slave replication — the last database gap.
//
// A master's binlog records what changed; a running slave resolves, dials and
// authenticates against the master through the real network gates and copies
// the changed object's own files through its single write gate, one event per
// tick. Nothing here is decorative: the process is in the table, the files are
// on the disk, and a broken path really stops the position advancing.

// mysqlReplWorld installs MariaDB on two machines of the same LAN: the PC as
// master (its hostname resolves at home) and the NAS as slave.
func mysqlReplWorld(t *testing.T) (*core.World, *core.Device, *core.Device) {
	t.Helper()
	w := core.NewWorld()
	repairDNS(t, w)
	master := w.Devices["pc-alex"]
	slave := w.Devices["nas-alex"]
	for _, d := range []*core.Device{master, slave} {
		if out := run(t, w, d, "root", "apt update"); !strings.Contains(out, "Reading package lists... Done") {
			t.Fatalf("setup: apt update on %s failed:\n%s", d.Hostname, out)
		}
		if out := run(t, w, d, "root", "apt install mariadb"); !strings.Contains(out, "Setting up mariadb") && !strings.Contains(out, "Unpacking") {
			t.Fatalf("setup: install on %s failed:\n%s", d.Hostname, out)
		}
		if svc := d.Svc("mariadb"); svc == nil || svc.State != "running" {
			t.Fatalf("setup: mariadb service missing on %s: %+v", d.Hostname, svc)
		}
	}
	return w, master, slave
}

func replProcExists(d *core.Device, name string) bool {
	for _, p := range d.Procs {
		if p.Name == name {
			return true
		}
	}
	return false
}

// replCatchUp ticks until the slave has applied everything the master has, or
// fails — no wall clock anywhere, the tick is the only time.
func replCatchUp(t *testing.T, w *core.World, slave, master *core.Device) {
	t.Helper()
	for i := 0; i < 40; i++ {
		if slave.Repl != nil && master.Repl != nil && slave.Repl.MasterLogPos >= master.Repl.BinLogPos {
			return
		}
		w.Tick()
	}
	t.Fatalf("slave did not catch up: slave=%+v master pos=%d", slave.Repl, master.Repl.BinLogPos)
}

func TestMysqlReplicationCatchesUp(t *testing.T) {
	w, master, slave := mysqlReplWorld(t)

	run(t, w, master, "alex", "mysql -e 'CREATE DATABASE shop'")
	run(t, w, master, "alex", "mysql -e 'CREATE TABLE shop.items (id, name)'")
	if out := run(t, w, master, "alex", "mysql -e \"INSERT INTO shop.items VALUES (1, 'lamp')\""); !strings.Contains(out, "1 row(s)") {
		t.Fatalf("master insert failed:\n%s", out)
	}
	// the master's binlog is real state, one event per change
	if master.Repl == nil || master.Repl.BinLogPos != 3 || len(master.Repl.BinLog) != 3 {
		t.Fatalf("master binlog should hold 3 events, got %+v", master.Repl)
	}
	if out := run(t, w, master, "alex", "mysql -e 'SHOW MASTER STATUS'"); !strings.Contains(out, "mysql-bin.000001") || !strings.Contains(out, "\t3\t") {
		t.Fatalf("SHOW MASTER STATUS wrong:\n%s", out)
	}

	if out := run(t, w, slave, "alex", "mysql -e \"CHANGE MASTER TO MASTER_HOST='home-pc', MASTER_USER='alex', MASTER_PASSWORD='alex123'\""); !strings.Contains(out, "Query OK") {
		t.Fatalf("CHANGE MASTER failed:\n%s", out)
	}
	if out := run(t, w, slave, "alex", "mysql -e 'START SLAVE'"); !strings.Contains(out, "Query OK") {
		t.Fatalf("START SLAVE failed:\n%s", out)
	}
	if slave.Repl == nil || !slave.Repl.Running {
		t.Fatal("START SLAVE must set Running")
	}
	if !replProcExists(slave, "mariadb-repl") {
		t.Fatal("START SLAVE must add a ps-visible mariadb-repl process")
	}

	replCatchUp(t, w, slave, master)

	out := run(t, w, slave, "alex", "mysql -e 'SELECT * FROM shop.items'")
	if !strings.Contains(out, "lamp") {
		t.Fatalf("slave must see the replicated row:\n%s", out)
	}
	if slave.Repl.Lag != 0 || slave.Repl.MasterLogPos != master.Repl.BinLogPos {
		t.Fatalf("slave should be caught up: %+v", slave.Repl)
	}
	status := run(t, w, slave, "alex", "mysql -e 'SHOW SLAVE STATUS'")
	if !strings.Contains(status, "Yes\tYes") || !strings.Contains(status, "home-pc") {
		t.Fatalf("SHOW SLAVE STATUS wrong:\n%s", status)
	}
	if !strings.Contains(status, "\t0\t") {
		t.Fatalf("SHOW SLAVE STATUS should report Lag 0:\n%s", status)
	}
}

func TestMysqlReplicationSurvivesSave(t *testing.T) {
	w, master, slave := mysqlReplWorld(t)
	run(t, w, master, "alex", "mysql -e 'CREATE DATABASE shop'")
	run(t, w, master, "alex", "mysql -e 'CREATE TABLE shop.items (id, name)'")
	run(t, w, master, "alex", "mysql -e \"INSERT INTO shop.items VALUES (1, 'lamp')\"")
	run(t, w, slave, "alex", "mysql -e \"CHANGE MASTER TO MASTER_HOST='home-pc', MASTER_USER='alex', MASTER_PASSWORD='alex123'\"")
	run(t, w, slave, "alex", "mysql -e 'START SLAVE'")
	replCatchUp(t, w, slave, master)

	wantPos := slave.Repl.MasterLogPos
	wantBin := master.Repl.BinLogPos

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	bs, bm := back.Devices["nas-alex"], back.Devices["pc-alex"]
	if bs.Repl == nil || !bs.Repl.Running {
		t.Fatal("slave replication config must survive the save")
	}
	if bs.Repl.MasterHost != "home-pc" || bs.Repl.MasterUser != "alex" || bs.Repl.MasterPassword != "alex123" {
		t.Fatalf("slave connection info lost: %+v", bs.Repl)
	}
	if bs.Repl.MasterLogPos != wantPos {
		t.Fatalf("applied position lost: got %d want %d", bs.Repl.MasterLogPos, wantPos)
	}
	if bm.Repl == nil || bm.Repl.BinLogPos != wantBin || len(bm.Repl.BinLog) != wantBin {
		t.Fatalf("master binlog lost: %+v", bm.Repl)
	}

	// writes after the reload still replicate through the restored config
	run(t, back, bm, "alex", "mysql -e \"INSERT INTO shop.items VALUES (2, 'nail')\"")
	replCatchUp(t, back, bs, bm)
	out := run(t, back, back.Devices["nas-alex"], "alex", "mysql -e 'SELECT * FROM shop.items'")
	if !strings.Contains(out, "nail") {
		t.Fatalf("replication must continue after a reload:\n%s", out)
	}
}

func TestMysqlReplicationStopsWhenThreadKilled(t *testing.T) {
	w, master, slave := mysqlReplWorld(t)
	run(t, w, master, "alex", "mysql -e 'CREATE DATABASE shop'")
	run(t, w, master, "alex", "mysql -e 'CREATE TABLE shop.items (id, name)'")
	run(t, w, slave, "alex", "mysql -e \"CHANGE MASTER TO MASTER_HOST='home-pc', MASTER_USER='alex', MASTER_PASSWORD='alex123'\"")
	run(t, w, slave, "alex", "mysql -e 'START SLAVE'")
	// a change that is now pending on the slave
	run(t, w, master, "alex", "mysql -e \"INSERT INTO shop.items VALUES (1, 'lamp')\"")

	// kill the apply thread the way a player does
	if out := run(t, w, slave, "alex", "pkill mariadb-repl"); strings.Contains(out, "no process matches") {
		t.Fatalf("pkill should find the replication thread:\n%s", out)
	}
	pos := slave.Repl.MasterLogPos
	w.Tick()
	w.Tick()
	if slave.Repl.MasterLogPos != pos {
		t.Fatalf("a killed apply thread must not make progress: %d -> %d", pos, slave.Repl.MasterLogPos)
	}
	if !slave.Repl.Running {
		t.Fatal("killing the thread must leave Running as the operator set it")
	}

	if out := run(t, w, slave, "alex", "mysql -e 'STOP SLAVE'"); !strings.Contains(out, "Query OK") {
		t.Fatalf("STOP SLAVE failed:\n%s", out)
	}
	if slave.Repl.Running {
		t.Fatal("STOP SLAVE must clear Running")
	}
	if replProcExists(slave, "mariadb-repl") {
		t.Fatal("STOP SLAVE must leave no apply thread behind")
	}
}

func TestMysqlReplicationFailuresAreReal(t *testing.T) {
	masterSetup := func(w *core.World, master *core.Device) {
		run(t, w, master, "alex", "mysql -e 'CREATE DATABASE shop'")
		run(t, w, master, "alex", "mysql -e 'CREATE TABLE shop.items (id, name)'")
	}

	t.Run("bad password", func(t *testing.T) {
		w, master, slave := mysqlReplWorld(t)
		masterSetup(w, master)
		run(t, w, slave, "alex", "mysql -e \"CHANGE MASTER TO MASTER_HOST='home-pc', MASTER_USER='alex', MASTER_PASSWORD='wrong'\"")
		run(t, w, slave, "alex", "mysql -e 'START SLAVE'")
		w.Tick()
		if !strings.Contains(slave.Repl.LastError, "Access denied") {
			t.Fatalf("a bad password must record an access-denied error, got %q", slave.Repl.LastError)
		}
		if slave.Repl.MasterLogPos != 0 {
			t.Fatalf("no data may move with a bad password, got position %d", slave.Repl.MasterLogPos)
		}
		if _, ok := slave.FS.Read("/var/lib/mysql/shop/items.schema"); ok {
			t.Fatal("a rejected slave must not hold the master's files")
		}
	})

	t.Run("stopped master", func(t *testing.T) {
		w, master, slave := mysqlReplWorld(t)
		masterSetup(w, master)
		run(t, w, slave, "alex", "mysql -e \"CHANGE MASTER TO MASTER_HOST='home-pc', MASTER_USER='alex', MASTER_PASSWORD='alex123'\"")
		run(t, w, slave, "alex", "mysql -e 'START SLAVE'")
		if _, err := master.StopService("mariadb"); err != nil {
			t.Fatalf("stopping the master's mariadb: %v", err)
		}
		w.Tick()
		if !strings.Contains(slave.Repl.LastError, "refused") {
			t.Fatalf("a stopped master must be diagnosed as refused, got %q", slave.Repl.LastError)
		}
		if slave.Repl.MasterLogPos != 0 {
			t.Fatalf("nothing may apply while the master is down, got %d", slave.Repl.MasterLogPos)
		}
	})

	t.Run("unpowered master", func(t *testing.T) {
		w, master, slave := mysqlReplWorld(t)
		masterSetup(w, master)
		run(t, w, slave, "alex", "mysql -e \"CHANGE MASTER TO MASTER_HOST='home-pc', MASTER_USER='alex', MASTER_PASSWORD='alex123'\"")
		run(t, w, slave, "alex", "mysql -e 'START SLAVE'")
		master.MainsDropped = true
		w.Tick()
		if !strings.Contains(slave.Repl.LastError, "down") {
			t.Fatalf("an unpowered master must be diagnosed as down, got %q", slave.Repl.LastError)
		}
		if slave.Repl.MasterLogPos != 0 {
			t.Fatalf("nothing may apply from a dark master, got %d", slave.Repl.MasterLogPos)
		}
	})
}
