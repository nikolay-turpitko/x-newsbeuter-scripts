package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/feeds"
	"golang.org/x/net/html"
)

// TODO: Have no time right now to make it into more general tool, hardcoded most settings.

const (
	// maxDepth sets how deep to crowl.
	maxDepth = 3

	// numLinksToStop is a maximum links to be crowled (before filtering).
	// Note: this is a soft threshold to stop crowling, due buffering, crawler
	// will visit far more links.
	numLinksToStop = 3000

	// maxLinksPerTask is a threshold to split huge tasks.
	maxLinksPerTask = 200

	// timeout to wait for new links in aggregation loop.
	// Used as an anti-hangout watchdog timer.
	timeout = 10 * time.Minute

	// maxFetchAttempts is a maximum number of attempts to download page.
	maxFetchAttempts = 3

	// sleepBetweenFetchAttempts timeout to sleep between attempts to download page.
	// It will be doubled at every attempt.
	sleepBetweenFetchAttempts = 1 * time.Second
)

// Links with these words in URL will not be visited and stored.
var stopWords = []string{
	"login",
	"signup",
	"signin",
	"sign_in",
	"auth",
	"price",
	"pricing",
	"wikipedia",
	"instgram",
	"meetup",
	"download",
	"youtube",
	"video",
	"image",
	"count",
	"translator",
	"2015",
	"2016",
}

// Links with these words in URL will be visited, but not stored into the feed.
var filterStopWords = []string{
	"twitter",
	"ycombinator",
	"google",
	"bing",
	"yandex",
	"hh",
	"moikrug",
	"search", // ?
}

// Only links with any of these words in URL will be stored into the feed.
var filterRequiredWords = []string{
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

var (
	locationRx = regexp.MustCompile("{?i}remote|telecommute|удал.нн")
	positionRx = regexp.MustCompile("Golang|golang")
)

type hrefDescr struct {
	// item should be nil if content doesn't match to filter.
	item *feeds.Item
}

type task struct {
	depth int
	hrefs map[string]hrefDescr
}

func main() {
	log.SetOutput(os.Stderr)
	log.Println("RSS filter started")

	var wg sync.WaitGroup
	in := make(chan *task, numWorkerChannels)
	out := make(chan *task, numWorkerChannels)

	// Counter of tasks in work.
	tasksInWork := int64(0)

	// Send document from stdin to workers.
	go func() {
		for link, v := range processDocument(os.Stdin, nil) {
			// Initial jobs sent individually to better distribute over workers.
			in <- &task{0, map[string]hrefDescr{link: v}}
			atomic.AddInt64(&tasksInWork, 1)
		}
	}()

	// Start workers to process links.
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go collectLinks(in, out, &wg)
	}

	// Wait for new links from workers.
	links := aggregateCollectedLinks(in, out, &tasksInWork)
	wg.Wait()
	close(out)

	// Filter results.
	// Unfortunately, cannot filter during aggregation. Otherwise crawler
	// would vaste time visiting ads many times.
	for href := range links {
		remove := false
		// First check for stop words.
		for _, w := range filterStopWords {
			if strings.Contains(strings.ToLower(href), w) {
				remove = true
				break
			}
		}
		if !remove {
			remove = true
			// Then check for required words.
			for _, w := range filterRequiredWords {
				if strings.Contains(strings.ToLower(href), w) {
					remove = false
					break
				}
			}
		}
		if remove {
			delete(links, href)
		}
	}

	// Output result feed.
	if len(links) > 0 {
		t := time.Now()
		feedOut := feeds.Feed{
			Title:   "Custom job crawler feed",
			Link:    &feeds.Link{Href: ""},
			Updated: t,
			Created: t,
			Items:   make([]*feeds.Item, 0, len(links)),
		}
		for _, v := range links {
			if v.item != nil {
				feedOut.Items = append(feedOut.Items, v.item)
			}
		}
		feedOut.WriteAtom(os.Stdout)
	}

	log.Println("RSS filter finished")
}

func collectLinks(
	in <-chan *task,
	out chan<- *task,
	wg *sync.WaitGroup) {
	for t := range in {
		// There is a good chance that many links from close pages will overlap.
		// It's better to remove duplicates localy.
		collected := map[string]hrefDescr{}
		for href := range t.hrefs {
			rootURL, err := url.Parse(href)
			if err != nil {
				log.Println(err)
				continue
			}
			r, err := fetch(href)
			if err != nil {
				log.Println(err)
				continue
			}
			for link, v := range processDocument(r, rootURL) {
				collected[link] = v
			}
		}
		out <- &task{t.depth, collected}
	}
	wg.Done()
}

// fetch checks mime type and downloads page.
func fetch(href string) (io.Reader, error) {
	retry := 0
	sleepTimeout := sleepBetweenFetchAttempts
	var (
		resp *http.Response
		err  error
	)
	for {
		resp, err = http.Get(href)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusTooManyRequests || retry >= maxFetchAttempts {
			break
		}
		time.Sleep(sleepTimeout)
		retry++
		sleepTimeout *= 2
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status from %s: %s", href, resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = http.DetectContentType(body)
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(mt, "html") {
		return nil, fmt.Errorf("unknown mime type: %s, raw: %s", mt, ct)
	}
	body = prefixRx.ReplaceAll(body, []byte{})
	body = suffixRx.ReplaceAll(body, []byte{})
	return bytes.NewReader(body), nil
}

func processDocument(
	r io.Reader,
	rootURL *url.URL) (links map[string]hrefDescr) {
	links = map[string]hrefDescr{}
	p := customBluemondayPolicy()
	r = p.SanitizeReader(r)
	var copy bytes.Buffer
	r = io.TeeReader(r, &copy)
	z := html.NewTokenizer(r)
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if z.Err() != io.EOF {
				log.Println(z.Err())
			}
			return links
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
				// Parse then convert to string, to save canonised link (to eliminate some duplicates).
				l, err := url.Parse(string(v))
				if err != nil {
					log.Println(err)
					break ATTRS
				}
				l.Fragment = ""                          // Remove "#" part.
				l.Path = strings.TrimSuffix(l.Path, "/") // Remove trailing slash, can cause errors on rare sites, but eliminates some duplicates.
				if l.IsAbs() {
					if strings.HasPrefix(l.Scheme, "http") {
						href := l.String()
						links[href] = *makeHrefDescr(href, copy.Bytes())
					}
					break ATTRS
				}
				if rootURL == nil {
					break ATTRS
				}
				href := rootURL.ResolveReference(l).String()
				links[href] = *makeHrefDescr(href, copy.Bytes())
			}
		}
	}
	return links
}

func makeHrefDescr(href string, content []byte) *hrefDescr {
	if !filterByContent(content) {
		return &hrefDescr{}
	}
	return &hrefDescr{&feeds.Item{
		Title:       href,
		Link:        &feeds.Link{Href: href},
		Description: string(content),
		Id:          href,
		Created:     time.Now(),
	}}
}

func filterByContent(content []byte) bool {
	return locationRx.Match(content) && positionRx.Match(content)
}

func aggregateCollectedLinks(
	in chan<- *task,
	out <-chan *task,
	tasksInWork *int64) (links map[string]hrefDescr) {
	links = map[string]hrefDescr{}
	// Timer used to prevent hangouts.
	timer := time.NewTimer(timeout)
	for {
		select {
		case <-timer.C:
			// Hangout watchdog.
			close(in)
			return links
		case t := <-out:
			timer.Stop()
			atomic.AddInt64(tasksInWork, -1)
			// Save and visit new links.
			d := t.depth + 1
			freshLinks := map[string]hrefDescr{}
			for href, v := range t.hrefs {
				// If link is not in links map, and task's depth is less then
				// maxDepth, than send new task (with incremented depth) into in channel.
				if _, ok := links[href]; !ok {
					links[href] = v
					if d < maxDepth && len(links) < numLinksToStop {
						freshLinks[href] = v
					}
				}
			}
			if len(freshLinks) > 0 {
				if len(freshLinks) < maxLinksPerTask {
					in <- &task{d, freshLinks}
					atomic.AddInt64(tasksInWork, 1)
				} else {
					// Split job.
					i := 0
					var batch map[string]hrefDescr
					for href, v := range freshLinks {
						if i%maxLinksPerTask == 0 {
							if i > 0 {
								in <- &task{d, batch}
								atomic.AddInt64(tasksInWork, 1)
							}
							batch = make(map[string]hrefDescr, maxLinksPerTask)
						}
						batch[href] = v
						i++
					}
					if len(batch) > 0 {
						in <- &task{d, batch}
						atomic.AddInt64(tasksInWork, 1)
					}
				}
			}
			inWork := atomic.LoadInt64(tasksInWork)
			if inWork <= 0 {
				close(in)
				return links
			}
			timer.Reset(timeout)
		}
	}
	return links
}
