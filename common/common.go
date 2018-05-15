package common

import (
	"bytes"
	"log"
	"regexp"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

const (
	// Can be more then NumCPU, because workers are blocked on net IO.
	NumWorkers        = 10
	NumWorkerChannels = NumWorkers * 2
)

var (
	PrefixRx = regexp.MustCompile("^.*<body>")
	SuffixRx = regexp.MustCompile("</body>.*$")
)

func CleanupHTML(b []byte) []byte {
	node, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		log.Println(err)
		return b
	}
	var bb bytes.Buffer
	err = html.Render(&bb, node)
	if err != nil {
		log.Println(err)
		return b
	}
	return bb.Bytes()
}

func CustomBluemondayPolicy() *bluemonday.Policy {
	p := bluemonday.StrictPolicy()
	p.AllowStandardAttributes()
	p.AllowStandardURLs()
	p.AllowElements("article", "aside")
	p.AllowElements("section")
	p.AllowElements("summary")
	p.AllowElements("h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowElements("hgroup")
	p.AllowAttrs("cite").OnElements("blockquote")
	p.AllowElements("br", "div", "hr", "p", "span", "wbr")
	p.AllowAttrs("href").OnElements("a")
	p.AllowElements("abbr", "acronym", "cite", "code", "dfn", "em",
		"figcaption", "mark", "s", "samp", "strong", "sub", "sup", "var")
	p.AllowAttrs("cite").OnElements("q")
	p.AllowElements("b", "i", "pre", "small", "strike", "tt", "u")
	p.AllowLists()
	p.AllowTables()
	p.SkipElementsContent("select")
	return p
}
