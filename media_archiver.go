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

type MediaTarget struct {
	ID  int
	URL string
}

func initMediaDB(db *sql.DB) error {
	schema := `
	PRAGMA foreign_keys = ON;
	CREATE TABLE IF NOT EXISTS article_media (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		article_id    INTEGER NOT NULL,
		media_url     TEXT UNIQUE,
		media_type    TEXT,
		local_path    TEXT,
		file_hash     TEXT,
		downloaded_at DATETIME,
		FOREIGN KEY(article_id) REFERENCES articles(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_media_article_id ON article_media(article_id);
	`
	_, err := db.Exec(schema)
	return err
}

func main() {
	if err := os.MkdirAll("media", 0755); err != nil {
		fmt.Printf("[-] Failed to create media directory: %v\n", err)
		return
	}

	db, err := sql.Open("sqlite", "archive.db")
	if err != nil {
		fmt.Printf("[-] Failed to open DB: %v\n", err)
		return
	}
	defer db.Close()

	if err := initMediaDB(db); err != nil {
		fmt.Printf("[-] Schema init failed: %v\n", err)
		return
	}

	// Select articles targeting video or audio endpoints
	query := `
		SELECT id, url
		FROM articles
		WHERE url LIKE '%vi_video%' OR url LIKE '%vi_audio%'
		ORDER BY id ASC
	`
	rows, err := db.Query(query)
	if err != nil {
		fmt.Printf("[-] Query failed: %v\n", err)
		return
	}
	defer rows.Close()

	var targets []MediaTarget
	for rows.Next() {
		var t MediaTarget
		if err := rows.Scan(&t.ID, &t.URL); err == nil {
			targets = append(targets, t)
		}
	}

	fmt.Printf("[*] Loaded %d multimedia targets from archive.db\n", len(targets))

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
		Timeout: 120 * time.Second, // Extended timeout for larger video buffers
	}

	for i, target := range targets {
		fmt.Printf("[%d/%d] Inspecting #%d: %s\n", i+1, len(targets), target.ID, target.URL)

		mediaLinks, mediaType, err := extractMediaSources(client, target.URL)
		if err != nil {
			fmt.Printf("    [-] Failed parsing media page: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if len(mediaLinks) == 0 {
			fmt.Printf("    [!] No embedded source tags found (check if inline JS is required)\n")
			continue
		}

		for _, mURL := range mediaLinks {
			var exists int
			db.QueryRow("SELECT COUNT(*) FROM article_media WHERE media_url = ?", mURL).Scan(&exists)
			if exists > 0 {
				fmt.Printf("    [*] Skipping existing media: %s\n", filepath.Base(mURL))
				continue
			}

			fmt.Printf("    [*] Downloading stream: %s\n", mURL)
			localPath, hash, err := streamMediaToDisk(client, mURL)
			if err != nil {
				fmt.Printf("    [-] Download failed: %v\n", err)
				time.Sleep(2 * time.Second)
				continue
			}

			_, err = db.Exec(`
				INSERT INTO article_media (article_id, media_url, media_type, local_path, file_hash, downloaded_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, target.ID, mURL, mediaType, localPath, hash, time.Now().UTC())

			if err != nil {
				fmt.Printf("    [-] DB error: %v\n", err)
			} else {
				fmt.Printf("    [+] Archived #%d -> %s (%s)\n", target.ID, filepath.Base(localPath), hash[:10])
			}

			time.Sleep(3 * time.Second)
		}
	}

	fmt.Println("\n[+] Multimedia archival pass finished.")
}

func extractMediaSources(client *http.Client, pageURL string) ([]string, string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, "", err
	}

	baseURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, "", err
	}

	mediaType := "video"
	if strings.Contains(pageURL, "vi_audio") {
		mediaType = "audio"
	}

	var sources []string
	selector := "video source, video, audio source, audio"

	doc.Find(selector).Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if !exists || src == "" {
			return
		}

		relURL, err := url.Parse(src)
		if err != nil {
			return
		}
		absoluteURL := baseURL.ResolveReference(relURL).String()

		sources = append(sources, absoluteURL)
	})

	return sources, mediaType, nil
}

func streamMediaToDisk(client *http.Client, fileURL string) (string, string, error) {
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("status code: %d", resp.StatusCode)
	}

	ext := strings.ToLower(filepath.Ext(fileURL))
	if ext == "" || len(ext) > 5 || strings.Contains(ext, "?") {
		if strings.Contains(fileURL, "audio") {
			ext = ".mp3"
		} else {
			ext = ".mp4"
		}
	}

	tempFile, err := os.CreateTemp("media", "stream_*"+ext)
	if err != nil {
		return "", "", err
	}
	defer tempFile.Close()

	hasher := sha256.New()
	multiWriter := io.MultiWriter(tempFile, hasher)

	// Stream straight to disk and compute hash in a single pass
	if _, err := io.Copy(multiWriter, resp.Body); err != nil {
		os.Remove(tempFile.Name())
		return "", "", err
	}

	hashStr := hex.EncodeToString(hasher.Sum(nil))
	finalPath := filepath.Join("media", hashStr[:16]+ext)

	_ = tempFile.Close()
	if err := os.Rename(tempFile.Name(), finalPath); err != nil {
		return "", "", err
	}

	return finalPath, hashStr, nil
}
