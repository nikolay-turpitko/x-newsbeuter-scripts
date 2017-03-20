package main

import (
	// "fmt"
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// How deep to crowl.
const maxDepth = 4

// Maximum links crowled (before filtering).
// Note: this is a soft threshold to stop crowling,
// but due buffering, crowler will visit far more links.
const maxLinks = 400

// Timeout to wait new links.
const timeout = 4 * time.Second

type task struct {
	depth int
	href  string
	r     io.Reader
}

// Links with these words in URL will not be visited and stored.
var stopWords = []string{
	"login",
	"signup",
	"signin",
	"sign_in",
	"twitter",
	"ycombinator",
	"google",
	"youtube",
	"price",
	"pricing",
	"wikipedia",
	"instgram",
	"meetup",
	"download",
	"2015",
	"2016",
}

// Only links with any of these words in URL will be stored into the feed.
var filterWords = []string{
	"job",
	"work",
	"career",
	"vacan",
	"hire",
	"hiring",
	"join",
	"apply",
	"team",
	"open",
	"meet",
	"position",
	"offer",
	"company",
	"remote",
	"golang",
	"developer",
	"programmer",
	"engineer",
	"architect",
	"opportunit",
	"employ",
}

func main() {
	log.SetOutput(os.Stderr)
	log.Println("RSS filter started")

	var wg sync.WaitGroup
	numWorkerChannels := 2 * maxLinks * numWorkers
	in := make(chan *task, numWorkerChannels)
	out := make(chan *task, numWorkerChannels)

	// Send document from stdin to workers.
	in <- &task{0, "", os.Stdin}

	// Start workers to process links.
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go collectLinks(in, out, &wg)
	}

	// Wait for new links from workers.
	// If link is not in links map, and task's depth is less then
	// maxDepth, than send new task (with incremented depth) into channel.
	links := make(map[string]struct{})
	timer := time.NewTimer(timeout)
WAIT_LINKS:
	for {
		select {
		case <-timer.C:
			// Wait workers to finish their job and close out channel.
			close(in)
			break WAIT_LINKS
		case t := <-out:
			timer.Reset(timeout)
			// Save and visit new link.
			href := t.href
			if _, ok := links[href]; !ok {
				links[href] = struct{}{}
				d := t.depth + 1
				if d < maxDepth && len(links) < maxLinks {
					in <- &task{d, href, nil}
				}
			}
		}
	}
	wg.Wait()
	close(out)

	// Filter results.
	// Unfortunately, cannot filter during aggregation. Otherwise crawler
	// would vaste time visiting ads many times.
FILTER:
	for href := range links {
		for _, filter := range filterWords {
			if strings.Contains(strings.ToLower(href), filter) {
				continue FILTER
			}
		}
		delete(links, href)
	}
	sortedLinks := make([]string, 0, len(links))
	for href := range links {
		sortedLinks = append(sortedLinks, href)
	}
	sort.Strings(sortedLinks)

	// Output result feed.
	//writeOutFeed(feedIn, items)

	//TODO: replace with Atom feed
	//TODO: filter by article content
	//TODO: check mime-type
	for _, href := range sortedLinks {
		log.Println(href)
	}
	log.Println("RSS filter finished")
}

func collectLinks(
	in <-chan *task,
	out chan<- *task,
	wg *sync.WaitGroup) {
	for t := range in {
		href := t.href
		r := t.r
		if href == "" && r == nil {
			log.Fatal("either href or r should be provided")
		}
		var rootURL *url.URL
		if href != "" {
			var err error
			rootURL, err = url.Parse(href)
			if err != nil {
				log.Println(err)
			}
			if r == nil {
				r, err = fetch(href)
				if err != nil {
					log.Println(err)
					continue
				}
			}
		}
		processDocument(r, out, rootURL, t.depth)
	}
	wg.Done()
}

func fetch(href string) (io.Reader, error) {
	resp, err := http.Get(href)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	body = prefixRx.ReplaceAll(body, []byte{})
	body = suffixRx.ReplaceAll(body, []byte{})
	return bytes.NewReader(body), nil
}

func processDocument(
	r io.Reader,
	out chan<- *task,
	rootURL *url.URL,
	depth int) {
	p := customBluemondayPolicy()
	z := html.NewTokenizer(p.SanitizeReader(r))
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				log.Println(z.Err())
			}
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			tn, hasAttr := z.TagName()
			if !hasAttr {
				continue
			}
			if len(tn) != 1 || tn[0] != 'a' {
				continue
			}
		ATTRS:
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if string(k) != "href" || len(v) < 1 {
					continue
				}
				if v[0] == '#' {
					break ATTRS
				}
				for _, stop := range stopWords {
					if bytes.Contains(bytes.ToLower(v), []byte(stop)) {
						break ATTRS
					}
				}
				if bytes.HasPrefix(v, []byte("http")) {
					out <- &task{depth, string(v), nil}
					break ATTRS
				}
				if rootURL == nil {
					break ATTRS
				}
				l, err := url.Parse(string(v))
				if err != nil {
					log.Println(err)
					break ATTRS
				}
				if l.IsAbs() {
					break ATTRS
				}
				out <- &task{depth, rootURL.ResolveReference(l).String(), nil}
			}
		}
	}
}
