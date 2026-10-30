package main

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:wacalls.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	fmt.Println("== calls ==")
	rows, err := db.Query(`SELECT call_id, direction, peer, started_at, ended_at, end_reason, recording_ms FROM calls ORDER BY started_at DESC LIMIT 15`)
	if err != nil {
		fmt.Println("  err:", err)
		return
	}
	for rows.Next() {
		var id, dir, peer, reason string
		var started, ended, recms int64
		if err := rows.Scan(&id, &dir, &peer, &started, &ended, &reason, &recms); err != nil {
			panic(err)
		}
		fmt.Printf("  %s | %s | %s | reason=%q | dur=%dms | rec=%dms\n", id[:8], dir, peer, reason, ended-started, recms)
		fmt.Printf("      started=%s\n", time.UnixMilli(started).Format("2006-01-02 15:04:05"))
	}
	rows.Close()

	fmt.Println("== sessoes ==")
	rows, err = db.Query(`SELECT id, name, jid FROM wacalls_sessions`)
	if err != nil {
		fmt.Println("  err:", err)
	} else {
		for rows.Next() {
			var id, name, jid string
			_ = rows.Scan(&id, &name, &jid)
			fmt.Printf("  %s name=%q jid=%q\n", id[:8], name, jid)
		}
		rows.Close()
	}

	fmt.Println("== tabelas ==")
	rows, err = db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		fmt.Println("  err:", err)
	} else {
		for rows.Next() {
			var name string
			_ = rows.Scan(&name)
			fmt.Println("  " + name)
		}
		rows.Close()
	}

	fmt.Println("== lid_store (peer 554384052993) ==")
	rows, err = db.Query(`SELECT lid, pn FROM lid_store WHERE pn LIKE '554384052993%'`)
	if err != nil {
		fmt.Println("  err:", err)
	} else {
		found := false
		for rows.Next() {
			var lid, pn string
			if err := rows.Scan(&lid, &pn); err != nil {
				panic(err)
			}
			fmt.Printf("  lid=%s pn=%s\n", lid, pn)
			found = true
		}
		rows.Close()
		if !found {
			fmt.Println("  (nenhum)")
		}
	}
}
