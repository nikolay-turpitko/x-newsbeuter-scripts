package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/feeds"
	"github.com/mmcdole/gofeed"
)

func main() {
	log.SetOutput(os.Stderr)
	log.Println("RSS filter started")

	fp := gofeed.NewParser()
	feedIn, _ := fp.Parse(os.Stdin)

	in := make(chan *gofeed.Item, numWorkerChannels)
	out := make(chan *gofeed.Item, numWorkerChannels)
	done := make(chan struct{})

	// Start workers to process items (download content and filter).
	for i := 0; i < numWorkers; i++ {
		go processItems(in, out, done)
	}

	// Wait workers to finish their job and close out channel.
	go waitWorkers(done, out)

	// Send items from input feed to workers.
	go emitItems(feedIn.Items, in)

	// Accumulate items into out feed.
	items := accumulateItems(out)

	// Output result feed.
	writeOutFeed(feedIn, items)

	log.Println("RSS filter finished")
}

func emitItems(items []*gofeed.Item, in chan<- *gofeed.Item) {
	for _, item := range items {
		in <- item
	}
	close(in)
}

func waitWorkers(done <-chan struct{}, ch chan<- *gofeed.Item) {
	for i := 0; i < numWorkers; i++ {
		<-done
	}
	close(ch)
}

func processItems(
	in <-chan *gofeed.Item,
	out chan<- *gofeed.Item,
	done chan<- struct{}) {
	for item := range in {
		resp, err := http.Get(item.Link)
		if err != nil {
			log.Println(err)
			continue
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Println(err)
			continue
		}
		// Cleanup and append article body to description.
		body = cleanupHTML(body)
		body = prefixRx.ReplaceAll(body, []byte{})
		body = suffixRx.ReplaceAll(body, []byte{})
		p := customBluemondayPolicy()
		item.Description = fmt.Sprintf(
			"%s<br><hr><br>%s",
			p.SanitizeBytes(cleanupHTML([]byte(item.Description))),
			string(p.SanitizeBytes(body)))
		out <- item
	}
	done <- struct{}{}
}

func accumulateItems(ch <-chan *gofeed.Item) []*feeds.Item {
	items := []*feeds.Item{}
	for item := range ch {
		items = append(items, &feeds.Item{
			Title:       item.Title,
			Link:        &feeds.Link{Href: item.Link},
			Description: item.Description,
			Id:          item.GUID,
			Updated:     saneTime(item.UpdatedParsed),
			Created:     saneTime(item.PublishedParsed),
		})
	}
	return items
}

func writeOutFeed(feedIn *gofeed.Feed, items []*feeds.Item) {
	if len(items) > 0 {
		feedOut := feeds.Feed{
			Title:       fmt.Sprintf("%s - [enriched]", feedIn.Title),
			Link:        &feeds.Link{Href: feedIn.Link},
			Description: feedIn.Description,
			Updated:     saneTime(feedIn.UpdatedParsed),
			Created:     saneTime(feedIn.PublishedParsed),
			Items:       items,
		}
		feedOut.WriteAtom(os.Stdout)
	}
}

func saneTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
