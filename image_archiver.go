package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	_ "modernc.org/sqlite"
)

type ArticleTarget struct {
	ID  int
	URL string
}

func initImageDB(db *sql.DB) error {
	schema := `
	PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS article_images (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		article_id    INTEGER NOT NULL,
		image_url     TEXT UNIQUE,
		local_path    TEXT,
		file_hash     TEXT,
		downloaded_at DATETIME,
		FOREIGN KEY(article_id) REFERENCES articles(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_images_article_id ON article_images(article_id);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// Add images_checked tracking column if missing (ignores duplicate column error)
	_, _ = db.Exec(`ALTER TABLE articles ADD COLUMN images_checked INTEGER DEFAULT 0;`)
	return nil
}

func main() {
	if err := os.MkdirAll("images", 0755); err != nil {
		fmt.Printf("[-] Failed to create images dir: %v\n", err)
		return
	}

	db, err := sql.Open("sqlite", "archive.db")
	if err != nil {
		fmt.Printf("[-] Failed to open DB: %v\n", err)
		return
	}
	defer db.Close()

	if err := initImageDB(db); err != nil {
		fmt.Printf("[-] Schema init failed: %v\n", err)
		return
	}

	// Query only unchecked articles from detail_com routes
	query := `
		SELECT id, url
		FROM articles
		WHERE url LIKE '%detail_com/comde%'
		  AND (images_checked IS NULL OR images_checked = 0)
		ORDER BY id ASC
	`
	rows, err := db.Query(query)
	if err != nil {
		fmt.Printf("[-] Query failed: %v\n", err)
		return
	}
	defer rows.Close()

	var targets []ArticleTarget
	for rows.Next() {
		var t ArticleTarget
		if err := rows.Scan(&t.ID, &t.URL); err == nil {
			targets = append(targets, t)
		}
	}

	fmt.Printf("[*] Loaded %d unchecked articles to inspect for imagery...\n", len(targets))
	if len(targets) == 0 {
		fmt.Println("[+] All articles have already been checked.")
		return
	}

	proxyPort := os.Getenv("PSIPHON_PORT")
	if proxyPort == "" {
		proxyPort = "51291"
	}
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%s", proxyPort))

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyURL(proxyURL),
			DisableKeepAlives: true,
		},
		Timeout: 35 * time.Second,
	}

	totalSaved := 0

	for i, target := range targets {
		fmt.Printf("[%d/%d] Checking Article #%d: %s\n", i+1, len(targets), target.ID, target.URL)

		imgURLs, err := extractImageURLs(client, target.URL)
		if err != nil {
			fmt.Printf("    [-] Error extracting images: %v\n", err)
			time.Sleep(3 * time.Second)
			continue
		}

		for _, imgURL := range imgURLs {
			var exists int
			db.QueryRow("SELECT COUNT(*) FROM article_images WHERE image_url = ?", imgURL).Scan(&exists)
			if exists > 0 {
				continue
			}

			localPath, hash, err := downloadImage(client, imgURL)
			if err != nil {
				fmt.Printf("    [-] Download failed for %s: %v\n", imgURL, err)
				time.Sleep(2 * time.Second)
				continue
			}

			_, err = db.Exec(`
				INSERT INTO article_images (article_id, image_url, local_path, file_hash, downloaded_at)
				VALUES (?, ?, ?, ?, ?)
			`, target.ID, imgURL, localPath, hash, time.Now().UTC())

			if err != nil {
				fmt.Printf("    [-] DB insert error: %v\n", err)
			} else {
				fmt.Printf("    [+] Linked Article #%d -> %s (Hash: %s...)\n", target.ID, filepath.Base(localPath), hash[:10])
				totalSaved++
			}

			time.Sleep(1800 * time.Millisecond)
		}

		// Mark article checked so it won't be reprocessed on restart
		_, _ = db.Exec("UPDATE articles SET images_checked = 1 WHERE id = ?", target.ID)
		time.Sleep(500 * time.Millisecond)
	}

	fmt.Printf("\n[+] Done! Downloaded and linked %d new images to articles in archive.db.\n", totalSaved)
}

func extractImageURLs(client *http.Client, pageURL string) ([]string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	baseURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}

	var urls []string
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if !exists || src == "" {
			return
		}

		relURL, err := url.Parse(src)
		if err != nil {
			return
		}
		absoluteURL := baseURL.ResolveReference(relURL).String()

		lower := strings.ToLower(absoluteURL)
		if !strings.Contains(lower, "logo") && !strings.Contains(lower, "icon") && !strings.Contains(lower, "blank") {
			urls = append(urls, absoluteURL)
		}
	})

	return urls, nil
}

func downloadImage(client *http.Client, imgURL string) (string, string, error) {
	req, err := http.NewRequest("GET", imgURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	h := sha256.Sum256(data)
	hashStr := hex.EncodeToString(h[:])

	ext := strings.ToLower(filepath.Ext(imgURL))
	if ext == "" || len(ext) > 5 || strings.Contains(ext, "?") {
		ext = ".jpg"
	}

	fileName := hashStr[:16] + ext
	filePath := filepath.Join("images", fileName)

	err = os.WriteFile(filePath, data, 0644)
	return filePath, hashStr, err
}
