package server

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/metadb-project/metadb/cmd/metadb/cache"
	"github.com/metadb-project/metadb/cmd/metadb/command"
	"github.com/metadb-project/metadb/cmd/metadb/log"
	"github.com/metadb-project/metadb/cmd/metadb/sqlx"
)

func execCommandList(cl *command.CommandList, db sqlx.DB, track *cache.Track, cschema *cache.Schema, users *cache.Users) error {
	var clt []command.CommandList = partitionTxn(cl, cschema)
	for _, cc := range clt {
		if len(cc.Cmd) == 0 {
			continue
		}
		// exec schema changes
		if err := execCommandSchema(&cc.Cmd[0], db, track, cschema, users); err != nil {
			return fmt.Errorf("schema: %s", err)
		}
		err := execCommandListData(db, cc, cschema)
		if err != nil {
			return fmt.Errorf("data: %s", err)
		}
		// log confirmation
		for _, c := range cc.Cmd {
			logDebugCommand(&c)
		}
	}
	return nil
}

func execCommandSchema(c *command.Command, db sqlx.DB, track *cache.Track, schema *cache.Schema, users *cache.Users) error {
	if c.Op == command.DeleteOp {
		return nil
	}
	var err error
	var delta *deltaSchema
	if delta, err = findDeltaSchema(c, schema); err != nil {
		return err
	}
	// TODO can we skip adding the table if we confirm it in sysdb?
	if err = addTable(sqlx.NewTable(c.SchemaName, c.TableName), db, track, users); err != nil {
		return err
	}
	if err = execDeltaSchema(delta, c.SchemaName, c.TableName, db, schema); err != nil {
		return err
	}
	return nil
}

func execDeltaSchema(delta *deltaSchema, tschema string, tableName string, db sqlx.DB, schema *cache.Schema) error {
	var err error
	var col deltaColumnSchema
	//if len(delta.column) == 0 {
	//        log.Trace("table %s: no schema changes", util.JoinSchemaTable(tschema, tableName))
	//}
	for _, col = range delta.column {
		// Is this a new column (as opposed to a modification)?
		if col.newColumn {
			log.Trace("table %s.%s: new column: %s %s", tschema, tableName, col.name, command.DataTypeToSQL(col.newType, col.newTypeSize))
			t := &sqlx.Table{Schema: tschema, Table: tableName}
			if err = addColumn(t, col.name, col.newType, col.newTypeSize, db, schema); err != nil {
				return err
			}
			continue
		}
		// If the types are the same and they are varchar, both PostgreSQL and
		// Redshift can alter the column in place
		if col.oldType == col.newType && col.oldType == command.VarcharType {
			log.Trace("table %s.%s: alter column: %s %s", tschema, tableName, col.name, command.DataTypeToSQL(col.newType, col.newTypeSize))
			if err = alterColumnVarcharSize(sqlx.NewTable(tschema, tableName), col.name, col.newType, col.newTypeSize, db, schema); err != nil {
				return err
			}
			continue
		}
		// Otherwise we have a completely new type
		log.Trace("table %s.%s: rename column %s", tschema, tableName, col.name)
		if err = renameColumnOldType(sqlx.NewTable(tschema, tableName), col.name, col.newType, col.newTypeSize, db, schema); err != nil {
			return err
		}
		log.Trace("table %s.%s: new column %s %s", tschema, tableName, col.name, command.DataTypeToSQL(col.newType, col.newTypeSize))
		t := &sqlx.Table{Schema: tschema, Table: tableName}
		if err = addColumn(t, col.name, col.newType, col.newTypeSize, db, schema); err != nil {
			return err
		}
	}
	return nil
}

func execCommandListData(db sqlx.DB, cc command.CommandList, cschema *cache.Schema) error {
	// Begin txn
	tx, err := db.BeginTx()
	if err != nil {
		return fmt.Errorf("start transaction: %s", err)
	}
	defer func(tx *sql.Tx) {
		_ = tx.Rollback()
	}(tx)
	// Exec data
	for _, c := range cc.Cmd {
		// Extra check of varchar sizes to ensure size was adjusted and avoid silent data loss
		// due to optimization errors
		for _, col := range c.Column {
			if col.DType == command.VarcharType {
				schemaCol := cschema.Column(sqlx.NewColumn(c.SchemaName, c.TableName, col.Name))
				if schemaCol != nil && col.DTypeSize > schemaCol.CharMaxLen {
					// TODO Factor fatal error exit into function
					log.Fatal("internal error: schema varchar size not adjusted: %d > %d", col.DTypeSize, schemaCol.CharMaxLen)
					os.Exit(-1)
				}
			}
		}
		// Execute data part of command
		if err = execCommandData(&c, tx, db); err != nil {
			return fmt.Errorf("%s\n%v", err, c)
		}
	}
	// Commit txn
	log.Trace("commit txn")
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %s", err)
	}
	return nil
}

func partitionTxn(cl *command.CommandList, cschema *cache.Schema) []command.CommandList {
	var clt []command.CommandList
	newcl := new(command.CommandList)
	var c, lastc command.Command
	for _, c = range cl.Cmd {
		req := requiresSchemaChanges(&c, &lastc, cschema)
		if req {
			if len(newcl.Cmd) > 0 {
				clt = append(clt, *newcl)
				newcl = new(command.CommandList)
			}
		}
		newcl.Cmd = append(newcl.Cmd, c)
		lastc = c
	}
	if len(newcl.Cmd) > 0 {
		clt = append(clt, *newcl)
	}
	return clt
}

func requiresSchemaChanges(c, o *command.Command, cschema *cache.Schema) bool {
	if c.Op == command.DeleteOp || c.Op == command.TruncateOp {
		return false
	}
	if c.Op != o.Op || c.SchemaName != o.SchemaName || c.TableName != o.TableName {
		return true
	}
	if len(c.Column) != len(o.Column) {
		return true
	}
	for i, col := range c.Column {
		cc := sqlx.Column{Schema: c.SchemaName, Table: c.TableName, Column: col.Name}
		if col.Name != o.Column[i].Name || col.DType != o.Column[i].DType || col.SemanticType != o.Column[i].SemanticType || col.PrimaryKey != o.Column[i].PrimaryKey {
			return true
		}
		if col.DType == command.VarcharType {
			// Special case for varchar
			schemaCol := cschema.Column(&cc)
			if schemaCol == nil {
				return true
			}
			if col.DTypeSize > schemaCol.CharMaxLen {
				return true
			}
		} else {
			if col.DTypeSize != o.Column[i].DTypeSize {
				return true
			}
		}
	}
	return false
}

func execCommandData(c *command.Command, tx *sql.Tx, db sqlx.DB) error {
	switch c.Op {
	case command.MergeOp:
		return execMergeData(c, tx, db)
	case command.DeleteOp:
		return execDeleteData(c, tx, db)
	case command.TruncateOp:
		return execTruncateData(c, tx, db)
	default:
		return fmt.Errorf("unknown command op: %v", c.Op)
	}
}

func execMergeData(c *command.Command, tx *sql.Tx, db sqlx.DB) error {
	t := sqlx.Table{Schema: c.SchemaName, Table: c.TableName}
	// Check if current record is identical.
	ident, id, cf, err := isCurrentIdentical(c, tx, db, &t)
	if err != nil {
		return err
	}
	if ident {
		if cf == "false" {
			return updateRowCF(c, tx, db, &t, id)
		}
		return nil
	}
	exec := make([]string, 0)
	// Current table:
	// Delete the record.
	if id != "" {
		delcur := "DELETE FROM " + db.TableSQL(&t) + " WHERE __id='" + id + "'"
		exec = append(exec, delcur)
	}
	// Insert new record.
	var inscur strings.Builder
	inscur.WriteString("INSERT INTO " + db.TableSQL(&t) + "(__start")
	if c.Origin != "" {
		inscur.WriteString(",__origin")
	}
	for _, c := range c.Column {
		inscur.WriteString("," + db.IdentiferSQL(c.Name))
	}
	inscur.WriteString(")VALUES('" + c.SourceTimestamp + "'")
	if c.Origin != "" {
		inscur.WriteString(",'" + c.Origin + "'")
	}
	for _, c := range c.Column {
		inscur.WriteString("," + c.EncodedData)
	}
	inscur.WriteString(")")
	exec = append(exec, inscur.String())
	// History table:
	// Select matching current record in history table and mark as not current.
	var uphist strings.Builder
	uphist.WriteString("UPDATE " + db.HistoryTableSQL(&t) + " SET __cf=TRUE,__end='" + c.SourceTimestamp + "',__current=FALSE WHERE __id=(SELECT __id FROM " + db.HistoryTableSQL(&t) + " WHERE __current AND __origin='" + c.Origin + "'")
	if err = wherePKDataEqual(db, &uphist, c.Column); err != nil {
		return err
	}
	uphist.WriteString(" LIMIT 1)")
	exec = append(exec, uphist.String())
	// insert new record
	var inshist strings.Builder
	inshist.WriteString("INSERT INTO " + db.HistoryTableSQL(&t) + "(__start,__end,__current")
	if c.Origin != "" {
		inshist.WriteString(",__origin")
	}
	for _, c := range c.Column {
		inshist.WriteString("," + db.IdentiferSQL(c.Name))
	}
	inshist.WriteString(")VALUES('" + c.SourceTimestamp + "','9999-12-31 00:00:00Z',TRUE")
	if c.Origin != "" {
		inshist.WriteString(",'" + c.Origin + "'")
	}
	for _, c := range c.Column {
		inshist.WriteString("," + c.EncodedData)
	}
	inshist.WriteString(")")
	exec = append(exec, inshist.String())
	// Run SQL.
	err = db.ExecMultiple(tx, exec)
	if err != nil {
		return err
	}
	return nil
}

func updateRowCF(c *command.Command, tx *sql.Tx, db sqlx.DB, t *sqlx.Table, id string) error {
	// Current table:
	upcur := "UPDATE " + db.TableSQL(t) + " SET __cf=TRUE WHERE __id='" + id + "'"
	// History table: select matching current record in history table.
	var uphist strings.Builder
	uphist.WriteString("UPDATE " + db.HistoryTableSQL(t) + " SET __cf=TRUE WHERE __id=(SELECT __id FROM " + db.HistoryTableSQL(t) + " WHERE __current AND __origin='" + c.Origin + "'")
	if err := wherePKDataEqual(db, &uphist, c.Column); err != nil {
		return err
	}
	uphist.WriteString(" LIMIT 1)")
	// Run SQL.
	err := db.ExecMultiple(tx, []string{upcur, uphist.String()})
	if err != nil {
		return err
	}
	return nil
}

func isCurrentIdentical(c *command.Command, tx *sql.Tx, db sqlx.DB, t *sqlx.Table) (bool, string, string, error) {
	var b strings.Builder
	b.WriteString("SELECT * FROM " + db.TableSQL(t) + " WHERE __origin='" + c.Origin + "'")
	if err := wherePKDataEqual(db, &b, c.Column); err != nil {
		return false, "", "", err
	}
	b.WriteString(" LIMIT 1")
	rows, err := db.Query(tx, b.String())
	if err != nil {
		return false, "", "", err
	}
	cols, err := rows.Columns()
	if err != nil {
		return false, "", "", err
	}
	ptrs := make([]interface{}, len(cols))
	results := make([][]byte, len(cols))
	for i := range results {
		ptrs[i] = &results[i]
	}
	defer func(rows *sql.Rows) {
		_ = rows.Close()
	}(rows)
	var id, cf string
	attrs := make(map[string]*string)
	if rows.Next() {
		if err = rows.Scan(ptrs...); err != nil {
			return false, "", "", err
		}
		for i, r := range results {
			if r != nil {
				attr := cols[i]
				val := string(r)
				switch attr {
				case "__id":
					id = val
				case "__cf":
					cf = val
				case "__start":
				case "__end":
				case "__current":
				case "__origin":
				default:
					v := new(string)
					*v = val
					attrs[attr] = v
				}
			}
		}
	} else {
		return false, "", "", nil
	}
	for _, col := range c.Column {
		var cdata interface{}
		var ddata *string
		var cdatas, ddatas string
		cdata = col.Data
		if cdata != nil {
			cdatas = fmt.Sprintf("%v", cdata)
		}
		ddata = attrs[col.Name]
		if ddata != nil {
			ddatas = *ddata
		}
		if (cdata == nil && ddata != nil) || (cdata != nil && ddata == nil) {
			return false, id, cf, nil
		}
		if cdata != nil && ddata != nil && cdatas != ddatas {
			return false, id, cf, nil
		}
		delete(attrs, col.Name)
	}
	for _, v := range attrs {
		if v != nil {
			return false, id, cf, nil
		}
	}
	return true, id, cf, nil
}

func execDeleteData(c *command.Command, tx *sql.Tx, db sqlx.DB) error {
	t := sqlx.Table{Schema: c.SchemaName, Table: c.TableName}
	// Current table: delete the record.
	var delcur strings.Builder
	delcur.WriteString("DELETE FROM " + db.TableSQL(&t) + " WHERE __id=(SELECT __id FROM " + db.TableSQL(&t) + " WHERE __origin='" + c.Origin + "'")
	if err := wherePKDataEqual(db, &delcur, c.Column); err != nil {
		return err
	}
	delcur.WriteString(" LIMIT 1)")
	// History table: subselect matching current record in history table and mark as not current.
	var uphist strings.Builder
	uphist.WriteString("UPDATE " + db.HistoryTableSQL(&t) + " SET __cf=TRUE,__end='" + c.SourceTimestamp + "',__current=FALSE WHERE __id=(SELECT __id FROM " + db.HistoryTableSQL(&t) + " WHERE __current AND __origin='" + c.Origin + "'")
	if err := wherePKDataEqual(db, &uphist, c.Column); err != nil {
		return err
	}
	uphist.WriteString(" LIMIT 1)")
	// Run SQL.
	err := db.ExecMultiple(tx, []string{delcur.String(), uphist.String()})
	if err != nil {
		return err
	}
	return nil
}

func execTruncateData(c *command.Command, tx *sql.Tx, db sqlx.DB) error {
	t := sqlx.Table{Schema: c.SchemaName, Table: c.TableName}
	// Current table: delete all records from origin.
	delcur := "DELETE FROM " + db.TableSQL(&t) + " WHERE __origin='" + c.Origin + "'"
	// History table: mark as not current.
	var uphist strings.Builder
	uphist.WriteString("UPDATE " + db.HistoryTableSQL(&t) + " SET __cf=TRUE,__end='" + c.SourceTimestamp + "',__current=FALSE WHERE __current AND __origin='" + c.Origin + "'")
	// Run SQL.
	err := db.ExecMultiple(tx, []string{delcur, uphist.String()})
	if err != nil {
		return err
	}
	return nil
}

func wherePKDataEqual(db sqlx.DB, b *strings.Builder, columns []command.CommandColumn) error {
	first := true
	for _, c := range columns {
		if c.PrimaryKey != 0 {
			b.WriteString(" AND")
			if c.DType == command.JSONType {
				b.WriteString(" " + db.IdentiferSQL(c.Name) + "::text=" + c.EncodedData + "::text")
			} else {
				b.WriteString(" " + db.IdentiferSQL(c.Name) + "=" + c.EncodedData)
			}
			first = false
		}
	}
	if first {
		return fmt.Errorf("command missing primary key")
	}
	return nil
}

/*func checkRowExistsCurrent(c *command.Command, tx *sql.Tx, history bool) (int64, error) {
	var h string
	if history {
		h = "__"
	}
	var err error
	var pkey []command.CommandColumn = command.PrimaryKeyColumns(c.Column)
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, ""+
		"SELECT __id\n"+
		"    FROM %s\n"+
		"    WHERE __origin = '%s'", util.JoinSchemaTable(c.SchemaName, c.TableName+h), c.Origin)
	var col command.CommandColumn
	for _, col = range pkey {
		_, _ = fmt.Fprintf(&b, " AND\n        %s = %s", col.Name, command.SQLEncodeData(col.Data, col.DType, col.SemanticType))
	}
	if history {
		_, _ = fmt.Fprintf(&b, " AND\n        __current = TRUE")
	}
	_, _ = fmt.Fprintf(&b, ";")
	var q = b.String()
	var id int64
	err = tx.QueryRowContext(context.TODO(), q).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return 0, fmt.Errorf("%s:\n%s", err, q)
}
*/
