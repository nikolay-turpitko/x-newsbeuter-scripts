package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/feeds"
	"github.com/spf13/viper"
	"gopkg.in/xmlpath.v2"
)

// rawconfig is a map of root urls to settings (as in config file).
type rawconfig struct {
	RootURL     string `mapstructure:"root-url"`
	DateFormat  string `mapstructure:"date-format"`
	FeedTitle   string `mapstructure:"feed-title"`
	Item        string
	Title       string
	Link        string
	Description string
	Created     string
}

// config is a struct with parsed configuration.
type config struct {
	rootURL     string
	dateFormat  string
	feedTitle   string
	item        *xmlpath.Path
	title       *xmlpath.Path
	link        *xmlpath.Path
	description *xmlpath.Path
	created     *xmlpath.Path
}

var cfg config

func main() {
	//TODO: provide a flag maybe?
	//f, _ := os.Create("/tmp/nb-html2atom.log")
	//log.SetOutput(f)
	log.SetOutput(os.Stderr)
	log.Println("RSS filter started")

	// Parse config.
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s <config section name>\n", os.Args[0])
	}
	configSectionName := os.Args[1]
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatalln(err)
	}
	viper.AddConfigPath(".")
	viper.AddConfigPath(dir)
	log.Println(dir)
	viper.SetConfigName(filepath.Base(os.Args[0]))
	err = viper.ReadInConfig()
	if err != nil {
		log.Fatalln(err)
	}
	raw := rawconfig{}
	err = viper.UnmarshalKey(configSectionName, &raw)
	if err != nil {
		log.Fatalln(err)
	}

	cfg = config{
		rootURL:     raw.RootURL,
		dateFormat:  raw.DateFormat,
		feedTitle:   raw.FeedTitle,
		item:        xmlpath.MustCompile(raw.Item),
		title:       xmlpath.MustCompile(raw.Title),
		link:        xmlpath.MustCompile(raw.Link),
		description: xmlpath.MustCompile(raw.Description),
		created:     xmlpath.MustCompile(raw.Created),
	}

	// Parse html from stdin, cleanup, split to items.
	html, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		log.Fatalln(err)
	}
	html = cleanupHTML(html)

	root, err := xmlpath.ParseHTML(bytes.NewReader(html))
	if err != nil {
		log.Fatalln(err)
	}

	in := make(chan *xmlpath.Node, numWorkerChannels)
	out := make(chan *feeds.Item, numWorkerChannels)
	done := make(chan struct{})

	// Start workers to process items (download content and filter).
	for i := 0; i < numWorkers; i++ {
		go processItems(in, out, done)
	}

	// Wait workers to finish their job and close out channel.
	go waitWorkers(done, out)

	// Send items from input feed to workers.
	go emitItems(cfg.item.Iter(root), in)

	// Accumulate items into out feed.
	items := accumulateItems(out)

	// Output result feed.
	writeOutFeed(cfg.feedTitle, items)

	log.Println("RSS filter finished")
}

func emitItems(it *xmlpath.Iter, in chan<- *xmlpath.Node) {
	for it.Next() {
		in <- it.Node()
	}
	close(in)
}

func waitWorkers(done <-chan struct{}, ch chan<- *feeds.Item) {
	for i := 0; i < numWorkers; i++ {
		<-done
	}
	close(ch)
}

func processItems(
	in <-chan *xmlpath.Node,
	out chan<- *feeds.Item,
	done chan<- struct{}) {
	u, err := url.Parse(cfg.rootURL)
	if err != nil {
		log.Println(err)
	}
	for node := range in {
		description := extractXPathValue("description", cfg.description, node)
		link := extractXPathValue("link", cfg.link, node)
		id := link
		if link != "" && u != nil {
			l, err := url.Parse(link)
			if err != nil {
				log.Println(err)
			} else {
				u := u.ResolveReference(l)
				link = u.String()
			}
		}
		resp, err := http.Get(link)
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
		description = fmt.Sprintf(
			"%s<br><hr><br>%s",
			p.SanitizeBytes(cleanupHTML([]byte(description))),
			string(p.SanitizeBytes(body)))
		s := extractXPathValue("created", cfg.created, node)
		created, err := time.Parse(cfg.dateFormat, s)
		if err != nil {
			log.Println(err)
		}
		if created.Year() == 0 {
			t := time.Now()
			created = created.AddDate(t.Year(), 0, 0)
			if created.After(t) {
				created = created.AddDate(-1, 0, 0)
			}
		}
		item := &feeds.Item{
			Title:       extractXPathValue("title", cfg.title, node),
			Link:        &feeds.Link{Href: link},
			Description: description,
			Id:          id,
			Created:     created,
		}
		out <- item
	}
	done <- struct{}{}
}

func accumulateItems(ch <-chan *feeds.Item) []*feeds.Item {
	items := []*feeds.Item{}
	for item := range ch {
		items = append(items, item)
	}
	return items
}

func writeOutFeed(feedTitle string, items []*feeds.Item) {
	if len(items) > 0 {
		t := time.Now()
		feedOut := feeds.Feed{
			Title:   feedTitle,
			Link:    &feeds.Link{Href: cfg.rootURL},
			Updated: t,
			Created: t,
			Items:   items,
		}
		feedOut.WriteAtom(os.Stdout)
	}
}

func extractXPathValue(
	name string,
	path *xmlpath.Path,
	context *xmlpath.Node) string {
	v, found := path.String(context)
	if !found {
		log.Printf("'%s' not found\n", name)
	}
	return v
}
