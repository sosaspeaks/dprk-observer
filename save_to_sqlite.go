package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver
)

type VOKDispatch struct {
	Title string `json:"title"`
	Date  string `json:"date"`
	URL   string `json:"url"`
}

func main() {
	// 1. Open / create the SQLite database file
	db, err := sql.Open("sqlite", "archive.db")
	if err != nil {
		fmt.Printf("[-] Failed to open database: %v\n", err)
		return
	}
	defer db.Close()

	// 2. Create the articles table if it does not exist
	schema := `
	CREATE TABLE IF NOT EXISTS articles (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		url          TEXT UNIQUE,
		title        TEXT NOT NULL,
		publish_date TEXT,
		body_hash    TEXT,
		fetched_at   DATETIME
	);
	`
	if _, err := db.Exec(schema); err != nil {
		fmt.Printf("[-] Failed to create schema: %v\n", err)
		return
	}

	// 3. Read your scraped JSON file
	fileData, err := os.ReadFile("vok_dispatches.json")
	if err != nil {
		fmt.Printf("[-] Could not read JSON: %v\n", err)
		return
	}

	var dispatches []VOKDispatch
	if err := json.Unmarshal(fileData, &dispatches); err != nil {
		fmt.Printf("[-] Failed to parse JSON: %v\n", err)
		return
	}

	// 4. Prepare the INSERT statement
	stmt, err := db.Prepare(`
		INSERT INTO articles (url, title, publish_date, body_hash, fetched_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			title = excluded.title,
			publish_date = excluded.publish_date,
			fetched_at = excluded.fetched_at;
	`)
	if err != nil {
		fmt.Printf("[-] Failed to prepare statement: %v\n", err)
		return
	}
	defer stmt.Close()

	// 5. Insert each dispatch inside a transaction for high speed
	tx, err := db.Begin()
	if err != nil {
		fmt.Printf("[-] Failed to begin transaction: %v\n", err)
		return
	}

	txStmt := tx.Stmt(stmt)
	inserted := 0

	for _, d := range dispatches {
		// Skip empty links or bare language selector rows
		if d.URL == "" || d.Date == "" {
			continue
		}

		// Compute a SHA-256 fingerprint of title + URL for change verification
		h := sha256.Sum256([]byte(d.Title + d.URL))
		hashStr := hex.EncodeToString(h[:])

		_, err := txStmt.Exec(d.URL, d.Title, d.Date, hashStr, time.Now().UTC())
		if err != nil {
			fmt.Printf("[-] Error inserting %s: %v\n", d.Title, err)
			continue
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		fmt.Printf("[-] Failed to commit transaction: %v\n", err)
		return
	}

	fmt.Printf("[+] Successfully stored %d dispatches into archive.db!\n", inserted)
}
