package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// MariaDB (§52 管理数据库) — tables as files, SQL as the interface
//
// One database server per machine that installs the package: databases are
// directories under /var/lib/mysql, tables are a .schema file (column list)
// plus a .rows file (TAB-separated values), all owned by whoever created
// them. No separate account system: a database belongs to its creator, root
// goes everywhere, and remote logins reuse the device's own credentials —
// the same rule ssh, sftp and imap already enforce.
//
// The SQL subset is deliberate and documented, not a stub that accepts
// everything: SHOW/CREATE/DROP DATABASE, USE, SHOW TABLES, CREATE/DROP
// TABLE, INSERT, SELECT (columns, WHERE with = != <> < > <= >= LIKE, AND,
// LIMIT), UPDATE, DELETE. Everything is stored as text; a value containing
// a tab or newline is refused at INSERT/UPDATE time because the file format
// cannot hold it. Errors speak MySQL codes (1146 and friends) so skills
// transfer.
// ---------------------------------------------------------------------------

// mysqlDataDir is where every server in this world keeps its databases.
const mysqlDataDir = "/var/lib/mysql"

// mysqlBinLogFile is the name of every server's single binlog file. The model
// has one file per server, so rotation is not needed and not faked.
const mysqlBinLogFile = "mysql-bin.000001"

// mysqlErr renders a MySQL-coded error: the number is the diagnostic players
// already know, the words say what this world actually checked.
func mysqlErr(code int, sqlstate, format string, a ...any) error {
	return fmt.Errorf("ERROR %d (%s): %s", code, sqlstate, fmt.Sprintf(format, a...))
}

// mysqlEnsureRepl lazily creates the replication state, the way the server
// creates its binlog on first write. The binlog file name is server policy,
// not something a player sets.
func mysqlEnsureRepl(d *Device) *ReplState {
	if d.Repl == nil {
		d.Repl = &ReplState{}
	}
	if d.Repl.BinLogFile == "" {
		d.Repl.BinLogFile = mysqlBinLogFile
	}
	return d.Repl
}

// mysqlLogEvent records one successful change on the server's own binlog: the
// position is the event's identity, and DB/Table/Op name the object whose real
// files carry the change. A slave replays these against the same files.
func mysqlLogEvent(d *Device, db, table, op string) {
	r := mysqlEnsureRepl(d)
	r.BinLogPos++
	r.BinLog = append(r.BinLog, BinLogEvent{Pos: r.BinLogPos, DB: db, Table: table, Op: op})
}

// mysqlReplAllowed is the privilege rule for replication administration: root
// goes everywhere, and the machine's own owner may administer its server. Any
// other account is refused with the same 1227 a real server gives.
func mysqlReplAllowed(d *Device, u *User) error {
	if u == nil {
		return mysqlErr(1045, "28000", "Access denied for user ''@'localhost'")
	}
	if u.UID == 0 || (d.Owner != "" && u.Name == d.Owner) {
		return nil
	}
	return mysqlErr(1227, "42000", "Access denied; you need (at least one of) the SUPER, REPLICATION SLAVE privilege(s) for this operation")
}

// mysqlDBDir resolves a database directory, refusing names that would walk
// out of the datadir.
func mysqlDBDir(db string) (string, error) {
	db = strings.TrimSpace(db)
	if db == "" || strings.ContainsAny(db, "/.") {
		return "", mysqlErr(1102, "42000", "incorrect database name '%s'", db)
	}
	for _, c := range db {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$') {
			return "", mysqlErr(1102, "42000", "incorrect database name '%s'", db)
		}
	}
	return mysqlDataDir + "/" + db, nil
}

// mysqlCanUse answers whether u may touch a database: its creator, or root.
// The directory owner is the record — no second account table to disagree.
func mysqlCanUse(d *Device, u *User, db string) bool {
	if u == nil {
		return false
	}
	if u.UID == 0 {
		return true
	}
	dir, err := mysqlDBDir(db)
	if err != nil {
		return false
	}
	n, ok := d.FS.Get(dir)
	if ok && n.Owner == u.Name {
		return true
	}
	for _, line := range strings.Split(mysqlGrantFile(d, db), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == db && f[1] == u.Name {
			return true
		}
	}
	return false
}

func mysqlGrantPath(db string) string {
	dir, _ := mysqlDBDir(db)
	return dir + "/.grants"
}

func mysqlGrantFile(d *Device, db string) string {
	data, ok := d.FS.Read(mysqlGrantPath(db))
	if !ok {
		return ""
	}
	return string(data)
}

func mysqlGrantWrite(d *Device, u *User, db, grantee string) error {
	if !mysqlCanUse(d, u, db) {
		return mysqlErr(1044, "42000", "Access denied for database '%s'", db)
	}
	if _, err := mysqlDBDir(db); err != nil {
		return err
	}
	if d.FindUser(grantee) == nil {
		return mysqlErr(1133, "42000", "can't find user '%s'", grantee)
	}
	cur := mysqlGrantFile(d, db)
	key := db + " " + grantee
	for _, line := range strings.Split(cur, "\n") {
		if strings.TrimSpace(line) == key {
			return nil
		}
	}
	newc := strings.TrimRight(cur, "\n")
	if newc != "" {
		newc += "\n"
	}
	newc += key + "\n"
	return d.FS.WriteChecked(mysqlGrantPath(db), []byte(newc), u)
}

func mysqlRevokeWrite(d *Device, u *User, db, grantee string) error {
	if !mysqlCanUse(d, u, db) {
		return mysqlErr(1044, "42000", "Access denied for database '%s'", db)
	}
	cur := mysqlGrantFile(d, db)
	var out []string
	for _, line := range strings.Split(cur, "\n") {
		if strings.TrimSpace(line) == db+" "+grantee {
			continue
		}
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	newc := ""
	if len(out) > 0 {
		newc = strings.Join(out, "\n") + "\n"
	}
	return d.FS.WriteChecked(mysqlGrantPath(db), []byte(newc), u)
}

// mysqlListDBs reads the databases off the disk: a removed directory is a
// dropped database, nothing else pretends otherwise.
func mysqlListDBs(d *Device) []string {
	var out []string
	for _, p := range d.FS.List(mysqlDataDir) {
		if d.FS.IsDir(p) {
			out = append(out, p[strings.LastIndex(p, "/")+1:])
		}
	}
	sort.Strings(out)
	return out
}

// mysqlColumns reads a table's schema file.
func mysqlColumns(d *Device, db, table string) ([]string, error) {
	dir, err := mysqlDBDir(db)
	if err != nil {
		return nil, err
	}
	if !validTableName(table) {
		return nil, mysqlErr(1103, "42000", "incorrect table name '%s'", table)
	}
	data, ok := d.FS.Read(dir + "/" + table + ".schema")
	if !ok {
		return nil, mysqlErr(1146, "42S02", "Table '%s.%s' doesn't exist", db, table)
	}
	cols := strings.Split(strings.TrimRight(string(data), "\n"), ",")
	for i := range cols {
		cols[i] = strings.TrimSpace(cols[i])
	}
	return cols, nil
}

func validTableName(table string) bool {
	table = strings.TrimSpace(table)
	if table == "" {
		return false
	}
	for _, c := range table {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$') {
			return false
		}
	}
	return true
}

// mysqlRows reads every row of a table as column values in schema order.
func mysqlRows(d *Device, db, table string, cols []string) ([][]string, error) {
	dir, err := mysqlDBDir(db)
	if err != nil {
		return nil, err
	}
	data, ok := d.FS.Read(dir + "/" + table + ".rows")
	if !ok {
		return [][]string{}, nil
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		vals := strings.Split(line, "\t")
		for len(vals) < len(cols) {
			vals = append(vals, "NULL")
		}
		rows = append(rows, vals)
	}
	return rows, nil
}

// mysqlWriteRows stores rows back through the single write gate: a full or
// failing disk refuses the statement instead of half-writing it.
func mysqlWriteRows(d *Device, u *User, db, table string, rows [][]string) error {
	dir, err := mysqlDBDir(db)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join(r, "\t"))
		b.WriteString("\n")
	}
	return d.WriteGuest(dir+"/"+table+".rows", []byte(b.String()), u)
}

// mysqlLike matches a SQL LIKE pattern (% runs, _ single).
func mysqlLike(s, pattern string) bool {
	var rec func(si, pi int) bool
	rec = func(si, pi int) bool {
		for pi < len(pattern) {
			c := pattern[pi]
			switch c {
			case '%':
				for k := si; k <= len(s); k++ {
					if rec(k, pi+1) {
						return true
					}
				}
				return false
			case '_':
				if si >= len(s) {
					return false
				}
				si++
				pi++
			default:
				if si >= len(s) || s[si] != c {
					return false
				}
				si++
				pi++
			}
		}
		return si == len(s)
	}
	return rec(0, 0)
}

// mysqlCompare evaluates one WHERE predicate on a row: numeric when both
// sides parse as numbers (a string comparison would rank "80" above
// "1000"), lexical otherwise.
func mysqlCompare(cell, op, want string) bool {
	if op != "LIKE" {
		if cn, ok := mysqlNumber(cell); ok {
			if wn, ok := mysqlNumber(want); ok {
				switch op {
				case "=":
					return cn == wn
				case "!=", "<>":
					return cn != wn
				case "<":
					return cn < wn
				case ">":
					return cn > wn
				case "<=":
					return cn <= wn
				case ">=":
					return cn >= wn
				}
			}
		}
	}
	switch op {
	case "=":
		return cell == want
	case "!=", "<>":
		return cell != want
	case "<":
		return cell < want
	case ">":
		return cell > want
	case "<=":
		return cell <= want
	case ">=":
		return cell >= want
	case "LIKE":
		return mysqlLike(cell, want)
	}
	return false
}

// mysqlNumber parses a stored or literal value for numeric comparison.
func mysqlNumber(v string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// ---- statement parsing and execution ----

// mysqlSplitStatements cuts input on semicolons outside quotes.
func mysqlSplitStatements(input string) []string {
	var out []string
	var cur strings.Builder
	inStr := false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '\'' {
			if inStr && i+1 < len(input) && input[i+1] == '\'' {
				cur.WriteByte(c)
				cur.WriteByte(c)
				i++
				continue
			}
			inStr = !inStr
			cur.WriteByte(c)
			continue
		}
		if c == ';' && !inStr {
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// mysqlFields splits s on whitespace, keeping 'quoted ...' runs whole.
func mysqlFields(s string) []string {
	var out []string
	var cur strings.Builder
	inStr := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			if inStr && i+1 < len(s) && s[i+1] == '\'' {
				cur.WriteString("''")
				i++
				continue
			}
			inStr = !inStr
			cur.WriteByte(c)
			continue
		}
		if (c == ' ' || c == '\t' || c == '\n') && !inStr {
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return out
}

// mysqlUnquote strips one layer of single quotes, folding ” escapes.
func mysqlUnquote(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'"), true
	}
	return v, false
}

// mysqlLiteral reads one value: a quoted string, or a bare word/number.
func mysqlLiteral(v string) string {
	if s, ok := mysqlUnquote(v); ok {
		return s
	}
	return strings.TrimSpace(v)
}

// mysqlStripIdent removes backticks from identifiers.
func mysqlStripIdent(v string) string {
	v = strings.TrimSpace(v)
	return strings.Trim(strings.TrimSpace(v), "`")
}

// mysqlSplitList splits a comma list honouring quotes and parens depth.
func mysqlSplitList(s string) []string {
	var out []string
	var cur strings.Builder
	inStr, depth := false, 0
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			out = append(out, t)
		}
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			if inStr && i+1 < len(s) && s[i+1] == '\'' {
				cur.WriteString("''")
				i++
				continue
			}
			inStr = !inStr
			cur.WriteByte(c)
			continue
		}
		if !inStr {
			if c == '(' {
				depth++
			}
			if c == ')' {
				depth--
			}
			if c == ',' && depth == 0 {
				flush()
				continue
			}
		}
		cur.WriteByte(c)
	}
	flush()
	return out
}

// mysqlTableRef resolves [db.]table against the session database.
func mysqlTableRef(curDB, ref string) (db, table string, err error) {
	ref = strings.TrimSpace(ref)
	db, table = curDB, ref
	if i := strings.LastIndex(ref, "."); i >= 0 {
		db, table = mysqlStripIdent(ref[:i]), mysqlStripIdent(ref[i+1:])
	} else {
		table = mysqlStripIdent(table)
	}
	if db == "" {
		return "", "", mysqlErr(1046, "3D000", "No database selected")
	}
	if !validTableName(table) {
		return "", "", mysqlErr(1103, "42000", "incorrect table name '%s'", table)
	}
	return db, table, nil
}

type mysqlCond struct {
	col  string
	op   string
	want string
}

// mysqlParseWhere parses "col op value [AND ...]" (AND only, documented).
func mysqlParseWhere(s string) ([]mysqlCond, error) {
	var out []mysqlCond
	rest := strings.TrimSpace(s)
	for rest != "" {
		up := strings.ToUpper(rest)
		idx := strings.Index(up, " AND ")
		part := rest
		if idx >= 0 {
			// AND inside quotes does not split: re-scan honouring quotes
			part, rest = splitWhereAnd(rest)
		} else {
			rest = ""
		}
		f := mysqlFields(part)
		if len(f) < 3 {
			return nil, mysqlErr(1064, "42000", "syntax error near '%s' (only AND conditions here)", part)
		}
		op := strings.ToUpper(f[1])
		switch op {
		case "=", "!=", "<>", "<", ">", "<=", ">=", "LIKE":
		default:
			return nil, mysqlErr(1064, "42000", "unknown operator '%s'", f[1])
		}
		out = append(out, mysqlCond{col: mysqlStripIdent(f[0]), op: op, want: mysqlLiteral(strings.Join(f[2:], " "))})
		_ = idx
	}
	return out, nil
}

// splitWhereAnd cuts one AND-separated predicate honouring quotes.
func splitWhereAnd(s string) (head, tail string) {
	inStr := false
	up := strings.ToUpper(s)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			if inStr && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			inStr = !inStr
			continue
		}
		if !inStr && strings.HasPrefix(up[i:], " AND ") {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+5:])
		}
	}
	return strings.TrimSpace(s), ""
}

// mysqlMatchRow tests a row against parsed predicates.
func mysqlMatchRow(cols []string, row []string, conds []mysqlCond) (bool, error) {
	for _, c := range conds {
		i := indexOfCol(cols, c.col)
		if i < 0 {
			return false, mysqlErr(1054, "42S22", "Unknown column '%s'", c.col)
		}
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		if !mysqlCompare(cell, c.op, c.want) {
			return false, nil
		}
	}
	return true, nil
}

// MysqlExec runs ;-separated statements against a server, returning printed
// output and the session's (possibly changed) database. Authorization is the
// caller's business — the client resolves who before calling.
func MysqlExec(d *Device, u *User, curDB, input string) (string, string, error) {
	var out strings.Builder
	for _, stmt := range mysqlSplitStatements(input) {
		res, next, err := mysqlOne(d, u, curDB, stmt)
		curDB = next
		if res != "" {
			out.WriteString(res)
			if !strings.HasSuffix(res, "\n") {
				out.WriteString("\n")
			}
		}
		if err != nil {
			return out.String(), curDB, err
		}
	}
	return out.String(), curDB, nil
}

func mysqlOne(d *Device, u *User, curDB, stmt string) (string, string, error) {
	f := mysqlFields(stmt)
	if len(f) == 0 {
		return "", curDB, nil
	}
	kw := strings.ToUpper(f[0])
	rest := strings.TrimSpace(stmt[len(f[0]):])
	switch kw {
	case "BEGIN":
		return "Query OK, 0 rows affected\n", curDB, nil
	case "START":
		// START TRANSACTION stays the no-op it always was; START SLAVE is the
		// replication verb and must not be swallowed by it.
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rest)), "SLAVE") {
			return mysqlStartSlave(d, u, curDB)
		}
		return "Query OK, 0 rows affected\n", curDB, nil
	case "STOP":
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rest)), "SLAVE") {
			return mysqlStopSlave(d, u, curDB)
		}
		return "", curDB, mysqlErr(1064, "42000", "syntax error near 'STOP %s'", strings.TrimSpace(rest))
	case "CHANGE":
		return mysqlChangeMaster(d, u, curDB, rest)
	case "RESET":
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rest)), "SLAVE") {
			return mysqlResetSlave(d, u, curDB)
		}
		return "", curDB, mysqlErr(1064, "42000", "syntax error near 'RESET %s'", strings.TrimSpace(rest))
	case "COMMIT", "ROLLBACK":
		return "Query OK, 0 rows affected\n", curDB, nil
	case "SHOW":
		return mysqlShow(d, u, curDB, rest)
	case "CREATE":
		return mysqlCreate(d, u, curDB, rest)
	case "DROP":
		return mysqlDrop(d, u, curDB, rest)
	case "USE":
		db := mysqlStripIdent(rest)
		if _, err := mysqlDBDir(db); err != nil {
			return "", curDB, err
		}
		if _, ok := d.FS.Get(mysqlDataDir + "/" + db); !ok {
			return "", curDB, mysqlErr(1049, "42000", "Unknown database '%s'", db)
		}
		return "Database changed\n", db, nil
	case "INSERT":
		return mysqlInsert(d, u, curDB, rest)
	case "GRANT":
		return mysqlGrant(d, u, curDB, rest)
	case "REVOKE":
		return mysqlRevoke(d, u, curDB, rest)
	case "SELECT":
		return mysqlSelect(d, u, curDB, rest)
	case "UPDATE":
		return mysqlUpdate(d, u, curDB, rest)
	case "DELETE":
		return mysqlDelete(d, u, curDB, rest)
	}
	return "", curDB, mysqlErr(1064, "42000", "syntax error near '%s'", f[0])
}

func mysqlShow(d *Device, u *User, curDB, rest string) (string, string, error) {
	f := mysqlFields(rest)
	if len(f) == 0 {
		return "", curDB, mysqlErr(1064, "42000", "SHOW what? (DATABASES, TABLES, MASTER STATUS, SLAVE STATUS)")
	}
	switch strings.ToUpper(f[0]) {
	case "MASTER":
		if len(f) >= 2 && strings.ToUpper(f[1]) == "STATUS" {
			return mysqlShowMasterStatus(d, u, curDB)
		}
		return "", curDB, mysqlErr(1064, "42000", "SHOW MASTER STATUS")
	case "SLAVE":
		if len(f) >= 2 && strings.ToUpper(f[1]) == "STATUS" {
			return mysqlShowSlaveStatus(d, u, curDB)
		}
		return "", curDB, mysqlErr(1064, "42000", "SHOW SLAVE STATUS")
	case "DATABASES":
		var b strings.Builder
		b.WriteString("Database\n")
		for _, db := range mysqlListDBs(d) {
			b.WriteString(db + "\n")
		}
		return b.String(), curDB, nil
	case "TABLES":
		db := curDB
		if len(f) >= 3 && strings.ToUpper(f[1]) == "FROM" {
			db = mysqlStripIdent(f[2])
		}
		if !mysqlCanUse(d, u, db) {
			return "", curDB, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
		}
		dir, err := mysqlDBDir(db)
		if err != nil {
			return "", curDB, err
		}
		var names []string
		for _, p := range d.FS.List(dir) {
			if strings.HasSuffix(p, ".schema") {
				base := p[strings.LastIndex(p, "/")+1:]
				names = append(names, strings.TrimSuffix(base, ".schema"))
			}
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Tables_in_%s\n", db))
		for _, n := range names {
			b.WriteString(n + "\n")
		}
		return b.String(), curDB, nil
	}
	return "", curDB, mysqlErr(1064, "42000", "SHOW what? (DATABASES, TABLES, MASTER STATUS, SLAVE STATUS)")
}

// mysqlShowMasterStatus prints this server's binlog position, the same shape a
// real server prints: the file and the last assigned position.
func mysqlShowMasterStatus(d *Device, u *User, curDB string) (string, string, error) {
	if err := mysqlReplAllowed(d, u); err != nil {
		return "", curDB, err
	}
	file, pos := mysqlBinLogFile, 0
	if d.Repl != nil {
		if d.Repl.BinLogFile != "" {
			file = d.Repl.BinLogFile
		}
		pos = d.Repl.BinLogPos
	}
	var b strings.Builder
	b.WriteString("File\tPosition\tBinlog_Do_DB\tBinlog_Ignore_DB\n")
	b.WriteString(fmt.Sprintf("%s\t%d\t\t\n", file, pos))
	return b.String(), curDB, nil
}

// mysqlShowSlaveStatus prints the slave's connection and the position it has
// really applied. Lag is the number of events it is behind, so it reaches 0
// exactly when it has caught up.
func mysqlShowSlaveStatus(d *Device, u *User, curDB string) (string, string, error) {
	if err := mysqlReplAllowed(d, u); err != nil {
		return "", curDB, err
	}
	r := d.Repl
	if r == nil {
		r = &ReplState{}
	}
	io, sql := "No", "No"
	if r.Running {
		io, sql = "Yes", "Yes"
	}
	var b strings.Builder
	b.WriteString("Slave_IO_Running\tSlave_SQL_Running\tMaster_Host\tMaster_Log_File\tRead_Master_Log_Pos\tLag\tLast_Error\n")
	b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
		io, sql, r.MasterHost, r.MasterLogFile, r.MasterLogPos, r.Lag, r.LastError))
	return b.String(), curDB, nil
}

// mysqlChangeMaster records the upstream a slave will follow. The assignments
// use the same simple key=value style as the rest of this SQL subset, and an
// unknown key is refused rather than silently ignored.
func mysqlChangeMaster(d *Device, u *User, curDB, rest string) (string, string, error) {
	if err := mysqlReplAllowed(d, u); err != nil {
		return "", curDB, err
	}
	body := strings.TrimSpace(rest)
	if !strings.HasPrefix(strings.ToUpper(body), "MASTER") {
		return "", curDB, mysqlErr(1064, "42000", "CHANGE MASTER syntax: CHANGE MASTER TO MASTER_HOST=..., MASTER_USER=..., MASTER_PASSWORD=...")
	}
	body = strings.TrimSpace(body[len("MASTER"):])
	if !strings.HasPrefix(strings.ToUpper(body), "TO") {
		return "", curDB, mysqlErr(1064, "42000", "CHANGE MASTER needs TO")
	}
	body = strings.TrimSpace(body[len("TO"):])
	if body == "" {
		return "", curDB, mysqlErr(1064, "42000", "CHANGE MASTER needs at least one option")
	}
	r := mysqlEnsureRepl(d)
	for _, a := range mysqlSplitList(body) {
		eq := strings.Index(a, "=")
		if eq < 0 {
			return "", curDB, mysqlErr(1064, "42000", "bad CHANGE MASTER assignment '%s'", a)
		}
		key := strings.ToUpper(strings.TrimSpace(a[:eq]))
		val := mysqlLiteral(a[eq+1:])
		switch key {
		case "MASTER_HOST":
			r.MasterHost = val
		case "MASTER_USER":
			r.MasterUser = val
		case "MASTER_PASSWORD":
			r.MasterPassword = val
		case "MASTER_LOG_FILE":
			r.MasterLogFile = val
		case "MASTER_LOG_POS":
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil || n < 0 {
				return "", curDB, mysqlErr(1064, "42000", "MASTER_LOG_POS must be a non-negative number")
			}
			r.MasterLogPos = n
		default:
			return "", curDB, mysqlErr(1064, "42000", "unknown CHANGE MASTER option '%s'", key)
		}
	}
	d.Logf("info", "mariadb", "slave configured for master %s at position %d", r.MasterHost, r.MasterLogPos)
	return "Query OK, 0 rows affected\n", curDB, nil
}

// mysqlStartSlave sets the slave running and starts its apply thread. Without
// a configured master there is nothing to follow.
func mysqlStartSlave(d *Device, u *User, curDB string) (string, string, error) {
	if err := mysqlReplAllowed(d, u); err != nil {
		return "", curDB, err
	}
	r := mysqlEnsureRepl(d)
	if r.MasterHost == "" {
		return "", curDB, mysqlErr(1236, "HY000", "START SLAVE: no MASTER_HOST configured (run CHANGE MASTER TO first)")
	}
	r.Running = true
	r.LastError = ""
	if d.W != nil {
		d.W.startReplProc(d)
	}
	d.Logf("info", "mariadb", "slave started, following %s", r.MasterHost)
	return "Query OK, 0 rows affected\n", curDB, nil
}

// mysqlStopSlave stops applying and removes the apply thread. The configured
// master and the applied position are kept, so a later START resumes.
func mysqlStopSlave(d *Device, u *User, curDB string) (string, string, error) {
	if err := mysqlReplAllowed(d, u); err != nil {
		return "", curDB, err
	}
	r := mysqlEnsureRepl(d)
	r.Running = false
	if d.W != nil {
		d.W.stopReplProc(d)
	}
	d.Logf("info", "mariadb", "slave stopped")
	return "Query OK, 0 rows affected\n", curDB, nil
}

// mysqlResetSlave forgets the upstream and the applied position. The replicated
// data stays on disk, as RESET SLAVE leaves it.
func mysqlResetSlave(d *Device, u *User, curDB string) (string, string, error) {
	if err := mysqlReplAllowed(d, u); err != nil {
		return "", curDB, err
	}
	r := mysqlEnsureRepl(d)
	r.Running = false
	r.MasterHost, r.MasterUser, r.MasterPassword = "", "", ""
	r.MasterLogFile, r.MasterLogPos = "", 0
	r.LastError, r.Lag = "", 0
	if d.W != nil {
		d.W.stopReplProc(d)
	}
	return "Query OK, 0 rows affected\n", curDB, nil
}

func mysqlCreate(d *Device, u *User, curDB, rest string) (string, string, error) {
	f := mysqlFields(rest)
	if len(f) < 2 {
		return "", curDB, mysqlErr(1064, "42000", "CREATE what? (DATABASE, TABLE)")
	}
	switch strings.ToUpper(f[0]) {
	case "DATABASE":
		db := mysqlStripIdent(strings.Join(f[1:], " "))
		dir, err := mysqlDBDir(db)
		if err != nil {
			return "", curDB, err
		}
		if _, ok := d.FS.Get(dir); ok {
			return "", curDB, mysqlErr(1007, "HY000", "Can't create database '%s'; database exists", db)
		}
		// the datadir is a sticky box like /tmp: anyone may create a
		// database (owned by its creator), only the owner or root may use
		// or drop it — creation is harmless, use is gated
		d.FS.MkdirAll(dir, 0755, u.Name, u.Name)
		d.Logf("info", "mariadb", "%s created database %s", u.Name, db)
		mysqlLogEvent(d, db, "", "CREATE DATABASE")
		return "Query OK, 1 row affected\n", curDB, nil
	case "TABLE":
		paren := strings.Index(rest, "(")
		if paren < 0 {
			return "", curDB, mysqlErr(1064, "42000", "CREATE TABLE needs (columns)")
		}
		head := strings.TrimSpace(rest[len("TABLE"):paren])
		db, table, err := mysqlTableRef(curDB, head)
		if err != nil {
			return "", curDB, err
		}
		if !mysqlCanUse(d, u, db) {
			return "", curDB, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
		}
		dir, _ := mysqlDBDir(db)
		if _, ok := d.FS.Get(dir); !ok {
			return "", curDB, mysqlErr(1049, "42000", "Unknown database '%s'", db)
		}
		body := strings.TrimSpace(rest[paren:])
		if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
			return "", curDB, mysqlErr(1064, "42000", "malformed column list")
		}
		var cols []string
		for _, c := range mysqlSplitList(body[1 : len(body)-1]) {
			c = mysqlStripIdent(strings.Fields(c)[0])
			if c == "" {
				return "", curDB, mysqlErr(1064, "42000", "malformed column list")
			}
			cols = append(cols, c)
		}
		if len(cols) == 0 {
			return "", curDB, mysqlErr(1064, "42000", "a table needs at least one column")
		}
		if _, err := mysqlColumns(d, db, table); err == nil {
			return "", curDB, mysqlErr(1050, "42S01", "Table '%s' already exists", table)
		}
		if err := d.WriteGuest(dir+"/"+table+".schema", []byte(strings.Join(cols, ",")+"\n"), u); err != nil {
			return "", curDB, err
		}
		d.Logf("info", "mariadb", "%s created table %s.%s", u.Name, db, table)
		mysqlLogEvent(d, db, table, "CREATE TABLE")
		return "Query OK, 0 rows affected\n", curDB, nil
	}
	return "", curDB, mysqlErr(1064, "42000", "CREATE what? (DATABASE, TABLE)")
}

func mysqlDrop(d *Device, u *User, curDB, rest string) (string, string, error) {
	f := mysqlFields(rest)
	if len(f) < 2 {
		return "", curDB, mysqlErr(1064, "42000", "DROP what? (DATABASE, TABLE)")
	}
	switch strings.ToUpper(f[0]) {
	case "DATABASE":
		db := mysqlStripIdent(strings.Join(f[1:], " "))
		dir, err := mysqlDBDir(db)
		if err != nil {
			return "", curDB, err
		}
		if _, ok := d.FS.Get(dir); !ok {
			return "", curDB, mysqlErr(1008, "HY000", "Can't drop database '%s'; database doesn't exist", db)
		}
		if !mysqlCanUse(d, u, db) {
			return "", curDB, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
		}
		d.FS.Remove(dir)
		d.Logf("info", "mariadb", "%s dropped database %s", u.Name, db)
		mysqlLogEvent(d, db, "", "DROP DATABASE")
		return "Query OK, 0 rows affected\n", curDB, nil
	case "TABLE":
		db, table, err := mysqlTableRef(curDB, strings.Join(f[1:], " "))
		if err != nil {
			return "", curDB, err
		}
		if !mysqlCanUse(d, u, db) {
			return "", curDB, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
		}
		if _, err := mysqlColumns(d, db, table); err != nil {
			return "", curDB, err
		}
		dir, _ := mysqlDBDir(db)
		d.FS.Remove(dir + "/" + table + ".schema")
		d.FS.Remove(dir + "/" + table + ".rows")
		mysqlLogEvent(d, db, table, "DROP TABLE")
		return "Query OK, 0 rows affected\n", curDB, nil
	}
	return "", curDB, mysqlErr(1064, "42000", "DROP what? (DATABASE, TABLE)")
}

func mysqlInsert(d *Device, u *User, curDB, rest string) (string, string, error) {
	up := strings.ToUpper(rest)
	if !strings.HasPrefix(up, "INTO ") {
		return "", curDB, mysqlErr(1064, "42000", "INSERT INTO what? (table [(cols)] VALUES ...)")
	}
	rest = strings.TrimSpace(rest[len("INTO "):])
	vi := strings.Index(strings.ToUpper(rest), "VALUES")
	if vi < 0 {
		return "", curDB, mysqlErr(1064, "42000", "INSERT needs VALUES")
	}
	target, values := strings.TrimSpace(rest[:vi]), strings.TrimSpace(rest[vi+len("VALUES"):])
	cols := []string{}
	if pi := strings.Index(target, "("); pi >= 0 {
		if !strings.HasSuffix(target, ")") {
			return "", curDB, mysqlErr(1064, "42000", "malformed column list")
		}
		for _, c := range mysqlSplitList(target[pi+1 : len(target)-1]) {
			cols = append(cols, mysqlStripIdent(c))
		}
		target = strings.TrimSpace(target[:pi])
	}
	db, table, err := mysqlTableRef(curDB, target)
	if err != nil {
		return "", curDB, err
	}
	if !mysqlCanUse(d, u, db) {
		return "", curDB, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
	}
	schema, err := mysqlColumns(d, db, table)
	if err != nil {
		return "", curDB, err
	}
	if len(cols) == 0 {
		cols = schema
	}
	// map the given columns onto schema positions
	pos := make([]int, len(cols))
	for i, c := range cols {
		pos[i] = -1
		for j, name := range schema {
			if strings.EqualFold(name, c) {
				pos[i] = j
			}
		}
		if pos[i] < 0 {
			return "", curDB, mysqlErr(1054, "42S22", "Unknown column '%s'", c)
		}
	}
	if !strings.HasPrefix(values, "(") {
		return "", curDB, mysqlErr(1064, "42000", "VALUES needs parenthesised rows")
	}
	// split top-level (...) groups
	var groups []string
	depth := 0
	cur := ""
	inStr := false
	for i := 0; i < len(values); i++ {
		c := values[i]
		if c == '\'' {
			if inStr && i+1 < len(values) && values[i+1] == '\'' {
				cur += "''"
				i++
				continue
			}
			inStr = !inStr
			cur += string(c)
			continue
		}
		if !inStr {
			if c == '(' {
				if depth == 0 {
					cur = ""
				} else {
					cur += string(c)
				}
				depth++
				continue
			}
			if c == ')' {
				depth--
				if depth == 0 {
					groups = append(groups, cur)
					cur = ""
					continue
				}
				cur += string(c)
				continue
			}
			if c == ',' && depth == 0 {
				continue
			}
		}
		cur += string(c)
	}
	rows, err := mysqlRows(d, db, table, schema)
	if err != nil {
		return "", curDB, err
	}
	for _, g := range groups {
		vals := mysqlSplitList(g)
		if len(vals) != len(cols) {
			return "", curDB, mysqlErr(1136, "21S01", "Column count doesn't match value count")
		}
		row := make([]string, len(schema))
		for i := range row {
			row[i] = "NULL"
		}
		for i, v := range vals {
			val := mysqlLiteral(v)
			if strings.ContainsAny(val, "\t\n") {
				return "", curDB, mysqlErr(1366, "HY000", "value holds a tab or newline the file format cannot store")
			}
			row[pos[i]] = val
		}
		rows = append(rows, row)
	}
	if err := mysqlWriteRows(d, u, db, table, rows); err != nil {
		return "", curDB, err
	}
	mysqlLogEvent(d, db, table, "INSERT")
	return fmt.Sprintf("Query OK, %d row(s) affected\n", len(groups)), curDB, nil
}

func mysqlWhereClause(rest, keyword string) (before, where string) {
	up := strings.ToUpper(rest)
	best := -1
	inStr := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '\'' {
			if inStr && i+1 < len(rest) && rest[i+1] == '\'' {
				i++
				continue
			}
			inStr = !inStr
			continue
		}
		if !inStr && strings.HasPrefix(up[i:], keyword) {
			best = i
			break
		}
	}
	if best < 0 {
		return strings.TrimSpace(rest), ""
	}
	return strings.TrimSpace(rest[:best]), strings.TrimSpace(rest[best+len(keyword):])
}

func mysqlGrant(d *Device, u *User, curDB, rest string) (string, string, error) {
	up := strings.ToUpper(rest)
	on := strings.Index(up, " ON ")
	to := strings.Index(up, " TO ")
	if on < 0 || to < 0 || to < on {
		return "", curDB, mysqlErr(1064, "42000", "GRANT syntax: GRANT ALL ON db.* TO user")
	}
	priv := strings.TrimSpace(rest[:on])
	dbpart := strings.TrimSpace(rest[on+len(" ON ") : to])
	grantee := mysqlStripIdent(strings.TrimSpace(rest[to+len(" TO "):]))
	if !strings.HasPrefix(strings.ToUpper(priv), "ALL") {
		return "", curDB, mysqlErr(1064, "42000", "only ALL privileges are modelled here")
	}
	dbpart = strings.TrimSuffix(dbpart, ".*")
	dbpart = strings.TrimSuffix(dbpart, "*")
	dbpart = mysqlStripIdent(strings.TrimSpace(dbpart))
	if dbpart == "" {
		dbpart = curDB
	}
	if err := mysqlGrantWrite(d, u, dbpart, grantee); err != nil {
		return "", curDB, err
	}
	d.Logf("info", "mariadb", "%s granted %s on %s to %s", u.Name, priv, dbpart, grantee)
	return "Query OK, 0 rows affected\n", curDB, nil
}

func mysqlRevoke(d *Device, u *User, curDB, rest string) (string, string, error) {
	up := strings.ToUpper(rest)
	on := strings.Index(up, " ON ")
	from := strings.Index(up, " FROM ")
	if on < 0 || from < 0 || from < on {
		return "", curDB, mysqlErr(1064, "42000", "REVOKE syntax: REVOKE ALL ON db.* FROM user")
	}
	dbpart := strings.TrimSpace(rest[on+len(" ON ") : from])
	grantee := mysqlStripIdent(strings.TrimSpace(rest[from+len(" FROM "):]))
	dbpart = strings.TrimSuffix(dbpart, ".*")
	dbpart = strings.TrimSuffix(dbpart, "*")
	dbpart = mysqlStripIdent(strings.TrimSpace(dbpart))
	if dbpart == "" {
		dbpart = curDB
	}
	if err := mysqlRevokeWrite(d, u, dbpart, grantee); err != nil {
		return "", curDB, err
	}
	return "Query OK, 0 rows affected\n", curDB, nil
}

func mysqlSelect(d *Device, u *User, curDB, rest string) (string, string, error) {
	up := strings.ToUpper(rest)
	fi := strings.Index(up, " FROM ")
	if fi < 0 {
		return "", curDB, mysqlErr(1064, "42000", "SELECT needs FROM")
	}
	collist, from := strings.TrimSpace(rest[:fi]), strings.TrimSpace(rest[fi+len(" FROM "):])
	from, whereStr := mysqlWhereClause(from, "WHERE")
	limit := -1
	if li := strings.LastIndex(strings.ToUpper(whereStr), " LIMIT "); li >= 0 {
		var n int
		if _, err := fmt.Sscanf(whereStr[li:], " LIMIT %d", &n); err != nil || n < 0 {
			return "", curDB, mysqlErr(1064, "42000", "bad LIMIT")
		}
		limit = n
		whereStr = strings.TrimSpace(whereStr[:li])
	} else if li := strings.LastIndex(strings.ToUpper(from), " LIMIT "); li >= 0 && whereStr == "" {
		var n int
		if _, err := fmt.Sscanf(from[li:], " LIMIT %d", &n); err != nil || n < 0 {
			return "", curDB, mysqlErr(1064, "42000", "bad LIMIT")
		}
		limit = n
		from = strings.TrimSpace(from[:li])
	}

	// JOIN support: "a JOIN b ON a.x = b.y" (INNER; LEFT/RIGHT/CROSS accepted
	// as the same inner reconciliation this world performs honestly). A plain
	// comma separates a cartesian product.
	leftSpec := from
	var rightSpec, onCond string
	cross := false
	if ji := indexFold(from, " JOIN "); ji >= 0 {
		leftSpec = strings.TrimSpace(from[:ji])
		rest := strings.TrimSpace(from[ji+len(" JOIN "):])
		// strip a leading join-type keyword that precedes JOIN
		for _, kw := range []string{"INNER", "LEFT", "RIGHT", "CROSS", "OUTER"} {
			if strings.HasPrefix(strings.ToUpper(rest), kw+" ") {
				rest = strings.TrimSpace(rest[len(kw):])
			}
		}
		// left side may carry a trailing join-type keyword (e.g. "t1 INNER")
		for _, kw := range []string{"INNER", "LEFT", "RIGHT", "CROSS", "OUTER"} {
			if strings.HasSuffix(strings.ToUpper(strings.TrimSpace(leftSpec)), " "+kw) {
				leftSpec = strings.TrimSpace(leftSpec[:len(leftSpec)-len(kw)-1])
			}
		}
		if oi := indexFold(rest, " ON "); oi >= 0 {
			rightSpec = strings.TrimSpace(rest[:oi])
			onCond = strings.TrimSpace(rest[oi+len(" ON "):])
		} else {
			rightSpec = rest
		}
	} else if strings.Contains(from, ",") {
		parts := strings.SplitN(from, ",", 2)
		leftSpec = strings.TrimSpace(parts[0])
		rightSpec = strings.TrimSpace(parts[1])
		cross = true
	}

	load := func(spec string) (string, string, []string, [][]string, error) {
		db, table, err := mysqlTableRef(curDB, spec)
		if err != nil {
			return "", "", nil, nil, err
		}
		if !mysqlCanUse(d, u, db) {
			return "", "", nil, nil, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
		}
		schema, err := mysqlColumns(d, db, table)
		if err != nil {
			return "", "", nil, nil, err
		}
		rows, err := mysqlRows(d, db, table, schema)
		if err != nil {
			return "", "", nil, nil, err
		}
		return db, table, schema, rows, nil
	}

	ldb, _, leftSchema, leftRows, err := load(leftSpec)
	if err != nil {
		return "", curDB, err
	}
	_ = ldb

	var combinedCols []string
	combinedCols = append(combinedCols, leftSchema...)
	var rightSchema []string
	var rightRows [][]string
	if rightSpec != "" {
		rdb, _, rs, rr, err := load(rightSpec)
		if err != nil {
			return "", curDB, err
		}
		_ = rdb
		rightSchema = rs
		rightRows = rr
		combinedCols = append(combinedCols, rightSchema...)
	}

	// ON condition indices
	onLeftIdx, onRightIdx := -1, -1
	if onCond != "" {
		parts := strings.SplitN(onCond, "=", 2)
		if len(parts) != 2 {
			return "", curDB, mysqlErr(1064, "42000", "JOIN needs ON a.col = b.col")
		}
		onLeftIdx = indexOfCol(leftSchema, strings.TrimSpace(parts[0]))
		onRightIdx = indexOfCol(rightSchema, strings.TrimSpace(parts[1]))
		if onLeftIdx < 0 || onRightIdx < 0 {
			return "", curDB, mysqlErr(1054, "42S22", "Unknown column in ON clause")
		}
	}

	var want []int
	if strings.TrimSpace(collist) == "*" {
		for i := range combinedCols {
			want = append(want, i)
		}
	} else {
		for _, c := range mysqlSplitList(collist) {
			found := indexOfCol(combinedCols, c)
			if found < 0 {
				return "", curDB, mysqlErr(1054, "42S22", "Unknown column '%s'", c)
			}
			want = append(want, found)
		}
	}
	var conds []mysqlCond
	if strings.TrimSpace(whereStr) != "" {
		conds, err = mysqlParseWhere(whereStr)
		if err != nil {
			return "", curDB, err
		}
	}
	var combinedRows [][]string
	if rightSpec == "" {
		for _, r := range leftRows {
			combinedRows = append(combinedRows, append([]string{}, r...))
		}
	} else {
		for _, lr := range leftRows {
			for _, rr := range rightRows {
				if onCond != "" {
					li := onLeftIdx
					ri := onRightIdx
					lv := ""
					if li < len(lr) {
						lv = lr[li]
					}
					rv := ""
					if ri < len(rr) {
						rv = rr[ri]
					}
					if lv != rv {
						continue
					}
				}
				combined := append([]string{}, lr...)
				combined = append(combined, rr...)
				combinedRows = append(combinedRows, combined)
			}
		}
	}
	_ = cross
	var b strings.Builder
	hdr := make([]string, len(want))
	for i, w := range want {
		hdr[i] = combinedCols[w]
	}
	b.WriteString(strings.Join(hdr, "\t") + "\n")
	n := 0
	for _, r := range combinedRows {
		if limit >= 0 && n >= limit {
			break
		}
		ok, err := mysqlMatchRow(combinedCols, r, conds)
		if err != nil {
			return "", curDB, err
		}
		if !ok {
			continue
		}
		line := make([]string, len(want))
		for i, w := range want {
			if w < len(r) {
				line[i] = r[w]
			} else {
				line[i] = "NULL"
			}
		}
		b.WriteString(strings.Join(line, "\t") + "\n")
		n++
	}
	return b.String(), curDB, nil
}

func indexOfCol(cols []string, name string) int {
	name = mysqlStripIdent(name)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	for j, c := range cols {
		if strings.EqualFold(mysqlStripIdent(c), name) {
			return j
		}
	}
	return -1
}

func indexFold(s, sub string) int {
	return strings.Index(strings.ToUpper(s), strings.ToUpper(sub))
}
func mysqlUpdate(d *Device, u *User, curDB, rest string) (string, string, error) {
	target, whereStr := mysqlWhereClause(rest, "WHERE")
	// target is "<table> SET a = b, ...": split the table from its assigns
	si := strings.Index(strings.ToUpper(target), " SET ")
	if si < 0 {
		return "", curDB, mysqlErr(1064, "42000", "UPDATE needs SET")
	}
	setStr := strings.TrimSpace(target[si+len(" SET "):])
	target = strings.TrimSpace(target[:si])
	db, table, err := mysqlTableRef(curDB, target)
	if err != nil {
		return "", curDB, err
	}
	if !mysqlCanUse(d, u, db) {
		return "", curDB, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
	}
	schema, err := mysqlColumns(d, db, table)
	if err != nil {
		return "", curDB, err
	}
	assigns := mysqlSplitList(setStr)
	type assign struct {
		col int
		val string
	}
	var list []assign
	for _, a := range assigns {
		eq := strings.Index(a, "=")
		if eq < 0 {
			return "", curDB, mysqlErr(1064, "42000", "bad SET assignment '%s'", a)
		}
		col := mysqlStripIdent(a[:eq])
		i := -1
		for j, name := range schema {
			if strings.EqualFold(name, col) {
				i = j
			}
		}
		if i < 0 {
			return "", curDB, mysqlErr(1054, "42S22", "Unknown column '%s'", col)
		}
		val := mysqlLiteral(a[eq+1:])
		if strings.ContainsAny(val, "\t\n") {
			return "", curDB, mysqlErr(1366, "HY000", "value holds a tab or newline the file format cannot store")
		}
		list = append(list, assign{i, val})
	}
	var conds []mysqlCond
	if strings.TrimSpace(whereStr) != "" {
		conds, err = mysqlParseWhere(whereStr)
		if err != nil {
			return "", curDB, err
		}
	}
	rows, err := mysqlRows(d, db, table, schema)
	if err != nil {
		return "", curDB, err
	}
	hit := 0
	for ri, r := range rows {
		ok, err := mysqlMatchRow(schema, r, conds)
		if err != nil {
			return "", curDB, err
		}
		if !ok {
			continue
		}
		for _, a := range list {
			for len(rows[ri]) <= a.col {
				rows[ri] = append(rows[ri], "NULL")
			}
			rows[ri][a.col] = a.val
		}
		hit++
	}
	if err := mysqlWriteRows(d, u, db, table, rows); err != nil {
		return "", curDB, err
	}
	mysqlLogEvent(d, db, table, "UPDATE")
	return fmt.Sprintf("Query OK, %d row(s) affected\n", hit), curDB, nil
}

func mysqlDelete(d *Device, u *User, curDB, rest string) (string, string, error) {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(strings.ToUpper(rest), "FROM ") {
		rest = strings.TrimSpace(rest[len("FROM "):])
	}
	target, whereStr := mysqlWhereClause(rest, "WHERE")
	db, table, err := mysqlTableRef(curDB, target)
	if err != nil {
		return "", curDB, err
	}
	if !mysqlCanUse(d, u, db) {
		return "", curDB, mysqlErr(1044, "42000", "Access denied for database '%s'", db)
	}
	schema, err := mysqlColumns(d, db, table)
	if err != nil {
		return "", curDB, err
	}
	var conds []mysqlCond
	if strings.TrimSpace(whereStr) != "" {
		conds, err = mysqlParseWhere(whereStr)
		if err != nil {
			return "", curDB, err
		}
	}
	rows, err := mysqlRows(d, db, table, schema)
	if err != nil {
		return "", curDB, err
	}
	keep := rows[:0]
	for _, r := range rows {
		ok, err := mysqlMatchRow(schema, r, conds)
		if err != nil {
			return "", curDB, err
		}
		if !ok {
			keep = append(keep, r)
		}
	}
	if err := mysqlWriteRows(d, u, db, table, keep); err != nil {
		return "", curDB, err
	}
	mysqlLogEvent(d, db, table, "DELETE")
	return fmt.Sprintf("Query OK, %d row(s) affected\n", len(rows)-len(keep)), curDB, nil
}
