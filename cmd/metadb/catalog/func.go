package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/metadb-project/metadb/cmd/metadb/dbx"
	"github.com/metadb-project/metadb/cmd/metadb/log"
	"github.com/metadb-project/metadb/cmd/metadb/util"
)

var functionDefs = [][]string{
	{"metadb_version()", `
CREATE OR REPLACE FUNCTION public.metadb_version() RETURNS text
    AS $$ SELECT 'Metadb ` + util.MetadbVersion + `' $$
    LANGUAGE SQL`},
	{"ps()", `
CREATE OR REPLACE FUNCTION public.ps() RETURNS TABLE(username text, state text, elapsed text, query text)
    AS $$
       SELECT usename::text username, state, to_char(now() - query_start, 'HH24:MI:SS') AS elapsed, query
           FROM pg_stat_activity
           WHERE leader_pid IS NULL AND pid <> pg_backend_pid() AND state <> 'idle'
           ORDER BY query_start
       $$
    LANGUAGE SQL`},
}

func CreateAllFunctions(dcsuper, dc *pgx.Conn, systemuser string) error {
	q := "GRANT CREATE, USAGE ON SCHEMA public TO " + systemuser
	_, err := dcsuper.Exec(context.TODO(), q)
	if err != nil {
		return fmt.Errorf("granting systemuser access to public schema: %v", err)
	}

	users, err := AllUsers(dc)
	if err != nil {
		return fmt.Errorf("accessing user list: %v", err)
	}

	for _, f := range functionDefs {
		err := createFunction(dc, f[0], f[1], users)
		if err != nil {
			return err
		}
	}

	for _, u := range users {
		q := "GRANT USAGE ON SCHEMA public TO " + u
		_, _ = dcsuper.Exec(context.TODO(), q)
	}

	log.Trace("created database functions")
	return nil
}

func createFunction(dc *pgx.Conn, fname, fdef string, users []string) error {
	tx, err := dc.Begin(context.TODO())
	if err != nil {
		return fmt.Errorf("starting transaction for function: %s: %v", fname, err)
	}
	defer dbx.Rollback(tx)

	_, err = tx.Exec(context.TODO(), fdef)
	if err != nil {
		return fmt.Errorf("creating function: %s: %v", fname, err)
	}

	q := "REVOKE EXECUTE ON FUNCTION public." + fname + " FROM public;"
	_, err = tx.Exec(context.TODO(), q)
	if err != nil {
		return fmt.Errorf("revoking public access to function: %s: %v", fname, err)
	}

	for _, u := range users {
		q := "GRANT EXECUTE ON FUNCTION public." + fname + " TO " + u
		_, _ = tx.Exec(context.TODO(), q)
	}

	err = tx.Commit(context.TODO())
	if err != nil {
		return err
	}
	return nil
}
