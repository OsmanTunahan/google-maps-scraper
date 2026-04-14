package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"
)

type Place struct {
	Name     string
	Address  string
	Rating   string
	Reviews  string
	Category string
	Hours    string
	Website  string
	Phone    string
}

type Job struct {
	URL string
}

type Result struct {
	Place Place
	Err   error
}

type Worker struct {
	id      int
	browser playwright.Browser
}

func NewWorker(id int, browser playwright.Browser) *Worker {
	return &Worker{id: id, browser: browser}
}

func (w *Worker) Start(jobs <-chan Job, results chan<- Result, wg *sync.WaitGroup) {
	defer wg.Done()

	context, err := w.browser.NewContext()
	if err != nil {
		log.Printf("[Worker %d] Error creating context: %v\n", w.id, err)
		return
	}
	page, err := context.NewPage()
	if err != nil {
		log.Printf("[Worker %d] Error creating page: %v\n", w.id, err)
		return
	}

	for job := range jobs {
		log.Printf("[Worker %d] Processing %s\n", w.id, job.URL)

		_, err := page.Goto(job.URL)
		if err != nil {
			results <- Result{Err: err}
			continue
		}

		time.Sleep(1500 * time.Millisecond)

		name, err := page.Locator(`//h1`).First().InnerText()
		if err != nil {
			log.Printf("[Worker %d] Error getting name for %s: %v\n", w.id, job.URL, err)
		}
		address, err := page.Locator(`//button[@data-item-id="address"]//div`).First().InnerText()
		if err != nil {
			log.Printf("[Worker %d] Error getting address for %s: %v\n", w.id, job.URL, err)
		}

		additionalData, err := page.Evaluate(`() => {
			const ratingElement = document.querySelector('span.ceis6c span[aria-hidden="true"]') || document.querySelector('div.fontBodyMedium span[aria-hidden="true"]');
			const reviewsElement = document.querySelector('span[aria-label*="yorum"]') || document.querySelector('button[aria-label*="yorum"]');
			const categoryElement = document.querySelector('button.DkEaL');
			const opensAtElement = document.querySelector('div.OMl5r') || document.querySelector('[data-item-id="oh"]');
			const websiteElement = document.querySelector('a[data-item-id="authority"]');
			const phoneElement = document.querySelector('button[data-item-id^="phone:tel:"]');

			return {
				rating: ratingElement ? ratingElement.innerText : "",
				reviews: reviewsElement ? reviewsElement.innerText : "",
				category: categoryElement ? categoryElement.innerText : "",
				hours: opensAtElement ? opensAtElement.innerText : "",
				website: websiteElement ? websiteElement.getAttribute('href') : "",
				phone: phoneElement ? phoneElement.innerText : ""
			};
		}`)

		var rating, reviews, category, hours, website, phone string
		if err == nil {
			if data, ok := additionalData.(map[string]interface{}); ok {
				rating, _ = data["rating"].(string)
				reviews, _ = data["reviews"].(string)
				category, _ = data["category"].(string)
				hours, _ = data["hours"].(string)
				website, _ = data["website"].(string)
				phone, _ = data["phone"].(string)
			}
		} else {
			log.Printf("[Worker %d] Error evaluating additional data for %s: %v\n", w.id, job.URL, err)
		}

		results <- Result{
			Place: Place{
				Name:     name,
				Address:  address,
				Rating:   rating,
				Reviews:  reviews,
				Category: category,
				Hours:    hours,
				Website:  website,
				Phone:    phone,
			},
		}
	}
}

func collectListingURLs(page playwright.Page, total int) []string {
	var urls []string

	feedSelector := `div[role="feed"]`
	_, err := page.WaitForSelector(feedSelector)
	if err != nil {
		log.Printf("Error waiting for feed: %v", err)
		return nil
	}

	locator := page.Locator(`a.hfpxzc`)

	lastCount := 0
	stalledCount := 0

	for {
		_, err := page.Evaluate(fmt.Sprintf(`document.querySelector('%s').scrollBy(0, 10000)`, feedSelector))
		if err != nil {
			log.Printf("Error scrolling: %v", err)
		}
		time.Sleep(2 * time.Second)

		count, _ := locator.Count()
		log.Printf("Collected %d listings...\n", count)

		if count >= total {
			break
		}

		if count == lastCount {
			stalledCount++
			if stalledCount > 5 {
				log.Println("Collection stalled, finishing with what we have.")
				break
			}
		} else {
			lastCount = count
			stalledCount = 0
		}
	}

	elements, _ := locator.All()

	for i := 0; i < len(elements) && i < total; i++ {
		href, _ := elements[i].GetAttribute("href")
		if href != "" {
			urls = append(urls, href)
		}
	}

	return urls
}

func cleanString(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\r' {
			b.WriteRune(' ')
			continue
		}
		if r >= 0xE000 && r <= 0xF8FF {
			continue
		}
		b.WriteRune(r)
	}
	res := b.String()
	for strings.Contains(res, "  ") {
		res = strings.ReplaceAll(res, "  ", " ")
	}
	return strings.TrimSpace(res)
}

func main() {
	var searchQuery string
	var totalResults int
	var outputPath string
	var appendResults bool

	flag.StringVar(&searchQuery, "search", "", "Search query for Google Maps (Required)")
	flag.StringVar(&searchQuery, "s", "", "Search query (Required)")
	flag.IntVar(&totalResults, "total", 20, "Number of results to scrape")
	flag.IntVar(&totalResults, "t", 20, "Number of results to scrape (shorthand)")
	flag.StringVar(&outputPath, "output", "result.csv", "Output CSV file path")
	flag.StringVar(&outputPath, "o", "result.csv", "Output CSV file path (shorthand)")
	flag.BoolVar(&appendResults, "append", false, "Append results to the output file instead of overwriting")
	flag.Parse()

	if searchQuery == "" {
		flag.Usage()
		os.Exit(1)
	}

	log.Printf("Starting scraper...")
	pw, err := playwright.Run()
	if err != nil {
		log.Fatalf("could not start playwright: %v", err)
	}

	log.Printf("Launching browser...")
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		log.Fatalf("could not launch browser: %v", err)
	}

	mainPage, err := browser.NewPage()
	if err != nil {
		log.Fatalf("could not create page: %v", err)
	}

	if _, err := mainPage.Goto("https://www.google.com/maps"); err != nil {
		log.Fatalf("could not goto: %v", err)
	}

	acceptButton := mainPage.Locator("//button//span[contains(text(), 'Accept all') or contains(text(), 'Tümünü kabul et')]")
	if count, _ := acceptButton.Count(); count > 0 {
		log.Println("Handling cookie consent...")
		if err := acceptButton.First().Click(); err != nil {
			log.Printf("Warning: could not click accept button: %v", err)
		}
		time.Sleep(1 * time.Second)
	}

	searchSelector := `input#searchboxinput, input[role="combobox"]`
	_, err = mainPage.WaitForSelector(searchSelector)
	if err != nil {
		log.Fatalf("could not find search box: %v", err)
	}

	if err := mainPage.Fill(searchSelector, searchQuery); err != nil {
		log.Fatalf("could not fill search box: %v", err)
	}
	if err := mainPage.Keyboard().Press("Enter"); err != nil {
		log.Fatalf("could not press enter: %v", err)
	}

	time.Sleep(5 * time.Second)

	urls := collectListingURLs(mainPage, totalResults)
	jobs := make(chan Job, len(urls))
	results := make(chan Result, len(urls))
	numWorkers := 5
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		worker := NewWorker(i+1, browser)
		wg.Add(1)
		go worker.Start(jobs, results, &wg)
	}
	for _, url := range urls {
		jobs <- Job{URL: url}
	}
	close(jobs)

	wg.Wait()
	close(results)

	var csvFile *os.File
	var openErr error
	if appendResults {
		csvFile, openErr = os.OpenFile(outputPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	} else {
		csvFile, openErr = os.Create(outputPath)
	}

	if openErr != nil {
		log.Fatalf("could not open csv file: %v", openErr)
	}
	defer csvFile.Close()

	isNewFile := true
	if fi, err := csvFile.Stat(); err == nil && fi.Size() > 0 {
		isNewFile = false
	}

	if isNewFile {
		csvFile.Write([]byte{0xEF, 0xBB, 0xBF})
	}

	writer := csv.NewWriter(csvFile)
	writer.Comma = ';'
	defer writer.Flush()

	if !appendResults || isNewFile {
		writer.Write([]string{"Name", "Address", "Rating", "Reviews", "Category", "Hours", "Website", "Phone"})
	}

	for res := range results {
		if res.Err != nil {
			log.Println("Error:", res.Err)
			continue
		}
		log.Println("Place:", res.Place.Name, "|", res.Place.Phone, "|", res.Place.Address)
		writer.Write([]string{
			cleanString(res.Place.Name),
			cleanString(res.Place.Address),
			cleanString(res.Place.Rating),
			cleanString(res.Place.Reviews),
			cleanString(res.Place.Category),
			cleanString(res.Place.Hours),
			cleanString(res.Place.Website),
			cleanString(res.Place.Phone),
		})
	}

	browser.Close()
	pw.Stop()
}
