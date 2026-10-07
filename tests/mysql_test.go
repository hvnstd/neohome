package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// MariaDB (§52 管理数据库) — installed software with a real engine: tables
// are files under /var/lib/mysql, SQL is parsed for real, access is the
// device's own accounts plus per-database ownership. Each test walks a happy
// path, a boundary and a recovery.

func mysqlWorld(t *testing.T) (*core.World, *core.Device) {
	t.Helper()
	w := core.NewWorld()
	repairDNS(t, w)
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "root", "apt update"); !strings.Contains(out, "Reading package lists... Done") {
		t.Fatalf("setup: apt update failed:\n%s", out)
	}
	if out := run(t, w, pc, "root", "apt install mariadb"); !strings.Contains(out, "Setting up mariadb") && !strings.Contains(out, "Unpacking") {
		t.Fatalf("setup: install failed:\n%s", out)
	}
	if svc := pc.Svc("mariadb"); svc == nil || svc.State != "running" {
		t.Fatalf("setup: mariadb service missing: %+v", svc)
	}
	return w, pc
}

func TestMysqlIsInstalledSoftware(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	if out := run(t, w, pc, "alex", "mysql -e 'SHOW DATABASES'"); !strings.Contains(out, "command not found") {
		t.Fatalf("mysql must not exist before install, got:\n%s", out)
	}
}

func TestMysqlCRUDFlow(t *testing.T) {
	w, pc := mysqlWorld(t)
	sh := func(line string) string { return run(t, w, pc, "alex", line) }

	if out := sh("mysql -e 'CREATE DATABASE shop'"); !strings.Contains(out, "Query OK") {
		t.Fatalf("create db failed:\n%s", out)
	}
	if out := sh("mysql -e 'CREATE TABLE shop.items (id, name, price)'"); !strings.Contains(out, "Query OK") {
		t.Fatalf("create table failed:\n%s", out)
	}
	if out := sh("mysql -e \"INSERT INTO shop.items VALUES (1, 'lamp', 2500), (2, 'nail', 80)\""); !strings.Contains(out, "2 row(s)") {
		t.Fatalf("insert failed:\n%s", out)
	}
	out := sh("mysql -e 'SELECT * FROM shop.items'")
	if !strings.Contains(out, "lamp") || !strings.Contains(out, "nail") {
		t.Fatalf("select * must return rows:\n%s", out)
	}
	out = sh("mysql -e \"SELECT name FROM shop.items WHERE price > 1000\"")
	if !strings.Contains(out, "lamp") || strings.Contains(out, "nail") {
		t.Fatalf("where must filter:\n%s", out)
	}
	out = sh("mysql -e \"UPDATE shop.items SET price = 3000 WHERE name = 'lamp'\"")
	if !strings.Contains(out, "1 row(s)") {
		t.Fatalf("update failed:\n%s", out)
	}
	out = sh("mysql -e \"DELETE FROM shop.items WHERE name = 'nail'\"")
	if !strings.Contains(out, "1 row(s)") {
		t.Fatalf("delete failed:\n%s", out)
	}
	out = sh("mysql -e 'SELECT name, price FROM shop.items'")
	if !strings.Contains(out, "3000") || strings.Contains(out, "nail") {
		t.Fatalf("final state wrong:\n%s", out)
	}
	// errors speak MySQL codes
	if out := sh("mysql -e 'SELECT * FROM shop.missing'"); !strings.Contains(out, "ERROR 1146") {
		t.Fatalf("missing table must 1146, got:\n%s", out)
	}
	if out := sh("mysql -e 'SELECT nope FROM shop.items'"); !strings.Contains(out, "ERROR 1054") {
		t.Fatalf("missing column must 1054, got:\n%s", out)
	}
	// USE persists within one invocation, tables are files on disk
	if out := sh("mysql -e 'USE shop; SHOW TABLES'"); !strings.Contains(out, "items") {
		t.Fatalf("use+show failed:\n%s", out)
	}
	if _, ok := pc.FS.Read("/var/lib/mysql/shop/items.schema"); !ok {
		t.Fatal("schema must be a real file")
	}
	if _, ok := pc.FS.Read("/var/lib/mysql/shop/items.rows"); !ok {
		t.Fatal("rows must be a real file")
	}
}

func TestMysqlPermissionsAreReal(t *testing.T) {
	w, pc := mysqlWorld(t)
	run(t, w, pc, "alex", "mysql -e 'CREATE DATABASE shop'")
	run(t, w, pc, "root", "useradd dev2")
	runWithStdin(t, w, pc, "root", "passwd dev2", "dev2pass", "dev2pass")

	// another account's database is not yours
	if out := run(t, w, pc, "dev2", "mysql -e 'SHOW TABLES FROM shop'"); !strings.Contains(out, "Access denied") {
		t.Fatalf("cross-account reads must be refused, got:\n%s", out)
	}
	// root goes everywhere
	if out := run(t, w, pc, "root", "mysql -e 'SHOW TABLES FROM shop'"); strings.Contains(out, "Access denied") {
		t.Fatalf("root must read any database, got:\n%s", out)
	}
	// ...and owns what it creates
	run(t, w, pc, "root", "mysql -e 'CREATE DATABASE ops'")
	if out := run(t, w, pc, "alex", "mysql -e 'SHOW TABLES FROM ops'"); !strings.Contains(out, "Access denied") {
		t.Fatalf("root's database must refuse alex, got:\n%s", out)
	}
}

func TestMysqlRemoteTCP(t *testing.T) {
	w, pc := mysqlWorld(t)
	nas := w.Devices["nas-alex"]
	run(t, w, pc, "alex", "mysql -e 'CREATE DATABASE shop'")
	// the NAS has no mariadb: not installed at all, which the command says
	if out := run(t, w, nas, "root", "mysql -h 10.77.1.11 -u alex -e 'SHOW DATABASES'"); !strings.Contains(out, "command not found") {
		t.Fatalf("mysql must be install-gated, got:\n%s", out)
	}
	// install it there too, then remote auth is the device's own password
	repairDNS(t, w)
	run(t, w, nas, "root", "apt update")
	run(t, w, nas, "root", "apt install mariadb")
	out := runWithStdin(t, w, pc, "alex", "mysql -h 10.77.1.30 -u alex -e 'SHOW DATABASES'", "wrong")
	if !strings.Contains(out, "Access denied") {
		t.Fatalf("wrong remote password must fail, got:\n%s", out)
	}
	out = runWithStdin(t, w, pc, "alex", "mysql -h 10.77.1.30 -u alex -e 'CREATE DATABASE remotedb'", "alex123")
	if !strings.Contains(out, "Query OK") {
		t.Fatalf("remote authed write failed:\n%s", out)
	}
	if _, ok := nas.FS.Read("/var/lib/mysql/remotedb"); !ok {
		t.Fatal("the remote database must exist on the NAS")
	}
	// stopping the daemon closes the port like any other service
	nas.StopService("mariadb")
	if out := run(t, w, pc, "alex", "mysql -h 10.77.1.30 -u alex -e 'SHOW DATABASES'"); !strings.Contains(out, "refused") && !strings.Contains(out, "connect") {
		t.Fatalf("a stopped daemon must refuse TCP, got:\n%s", out)
	}
}

func TestMysqlDataSurvivesSave(t *testing.T) {
	w, pc := mysqlWorld(t)
	run(t, w, pc, "alex", "mysql -e 'CREATE DATABASE shop'")
	run(t, w, pc, "alex", "mysql -e \"CREATE TABLE shop.items (id, name)\"")
	run(t, w, pc, "alex", "mysql -e \"INSERT INTO shop.items VALUES (1, 'lamp')\"")

	path := t.TempDir() + "/w.gob"
	if err := w.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := core.LoadWorld(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := run(t, back, back.Devices["pc-alex"], "alex", "mysql -e 'SELECT * FROM shop.items'")
	if !strings.Contains(out, "lamp") {
		t.Fatalf("rows must survive the save:\n%s", out)
	}
}
