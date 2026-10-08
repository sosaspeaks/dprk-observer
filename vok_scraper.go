package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type VOKDispatch struct {
	Title string `json:"title"`
	Date  string `json:"date"`
	URL   string `json:"url"`
}

func main() {
	proxyURL, _ := url.Parse("http://127.0.0.1:56827")
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 30 * time.Second,
	}

	target := "http://vok.rep.kp/index.php/home/main/en"
	fmt.Printf("[*] Harvesting Voice of Korea English dispatches: %s\n", target)

	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		fmt.Printf("[-] Failed to create request: %v\n", err)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) dprk-observer/0.1")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[-] Request failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		fmt.Printf("[-] Failed to parse HTML: %v\n", err)
		return
	}

	dateRegex := regexp.MustCompile(`\((\d{4}\.\d{1,2}\.\d{1,2})\)\s*$`)
	seen := make(map[string]bool)
	var dispatches []VOKDispatch

	// Specifically target clickable anchor tags
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())

		// Remove leading bullet characters
		text = strings.TrimPrefix(text, "·")
		text = strings.TrimPrefix(text, "-")
		text = strings.TrimSpace(text)

		// Ignore empty links or blocks with internal newlines (parent containers)
		if strings.Contains(text, "\n") || len(text) < 5 {
			return
		}

		href, exists := s.Attr("href")
		if !exists || href == "#" || strings.HasPrefix(href, "javascript:") {
			return
		}

		// Normalize relative links
		if strings.HasPrefix(href, "/") {
			href = "http://vok.rep.kp" + href
		}

		var dateStr string
		match := dateRegex.FindStringSubmatch(text)
		if len(match) == 2 {
			dateStr = match[1]
			text = strings.TrimSpace(dateRegex.ReplaceAllString(text, ""))
		}

		// Deduplicate
		if !seen[text] && len(text) > 3 {
			seen[text] = true
			dispatches = append(dispatches, VOKDispatch{
				Title: text,
				Date:  dateStr,
				URL:   href,
			})
		}
	})

	jsonData, err := json.MarshalIndent(dispatches, "", "  ")
	if err != nil {
		fmt.Printf("[-] Failed to encode JSON: %v\n", err)
		return
	}

	os.WriteFile("vok_dispatches.json", jsonData, 0644)
	fmt.Printf("[+] Successfully indexed %d clean dispatches to vok_dispatches.json\n", len(dispatches))
}
