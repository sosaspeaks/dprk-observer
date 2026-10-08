package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "archive.db")
	if err != nil {
		fmt.Printf("[-] Database open failed: %v\n", err)
		return
	}
	defer db.Close()

	var total int
	err = db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&total)
	if err != nil {
		fmt.Printf("[-] Count query failed: %v\n", err)
		return
	}

	fmt.Printf("[+] Total archived articles in archive.db: %d\n\n", total)
	fmt.Printf("%-4s | %-12s | %-16s | %s\n", "ID", "DATE", "SHA256 (PREFIX)", "TITLE")
	fmt.Println(strings.Repeat("-", 90))

	rows, err := db.Query("SELECT id, publish_date, body_hash, title FROM articles ORDER BY id DESC LIMIT 10")
	if err != nil {
		fmt.Printf("[-] Select query failed: %v\n", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var date, hash, title string
		if err := rows.Scan(&id, &date, &hash, &title); err != nil {
			continue
		}

		hashPrefix := hash
		if len(hashPrefix) > 14 {
			hashPrefix = hashPrefix[:14] + "..."
		}

		if len(title) > 50 {
			title = title[:47] + "..."
		}

		fmt.Printf("%-4d | %-12s | %-16s | %s\n", id, date, hashPrefix, title)
	}
}
