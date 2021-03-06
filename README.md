# x-newsbeuter-scripts

## Custom scripts for newsbeuter

A couple of custom scripts
([filters](https://newsbeuter.org/doc/newsbeuter.html#_scripts_and_filters_snownews_extensions))
for newsbeuter:

1. `nb-enrich-descr`: RSS|Atom -> Atom, with description supplemented with
   content of the article's link;
2. `nb-html2atom`: HTML -> Atom,  according to XPath rules.
3. `nb-job-crawler`: HTML -> Atom, crawl all links from initial page, filter
   with hardocded rules.

Both scripts were created to filter and monitor job feeds (to find remote job),
but they can be useful for other purposes.

Newsbeuter has an excellent feature, allowing to filter and combine feeds.
Unfortunately, it cannot use article's body in the filter expression (I think,
it's because full article is downloaded only when the user decides to open it).

But very often interesting keywords in the job feeds can be found only in the
body of the article. For example, sometimes "possible remotely" modestly
mentioned at the end of the full job's description. It can be very frustrating
to miss a good opportunity as well as very time consuming to open every item
just to find that it lacks required option.

As a workaround to overcome this issue I created __nb-enrich-descr__ script,
which parses RSS or Atom feed in its stdin, downloads content of the article and
appends it to the description. It also makes reading filtered feed more
convenient, because full job description is already downloaded and available
without additional keystrokes. Yes, this makes initial feed update more slow,
but it's a small price for the time saved on the manual feed screening.

Second script, __nb-html2atom__, was motivated by the fact, that not all job
marketplaces bothered to provide RSS or Atom feeds on their sites. But we don't
want to miss their offerings also. So, we could simply convert interesting html
page into the feed ourselves, using XPath rules. There are plenty of online
services for the task. Unfortunately, most of them require payment or dead. So,
it's more reliable to have simple local script for this task. 

Third script, __nb-job-crawler__, is a very crude crawler with hardocded
configuration. Though it crawls nicely, it very badly filters results.
Returns a lot of garbage links. More advanced logic required to filter out
false hits, but I have no time for it.

__Note__: Scripts created for my own use. So, no tests nor installer. If you
decide to use this scripts, you'll need a Go compiler. I refactored them to use 
`go get github.com/nikolay-turpitko/x-newsbeuter-scripts/cmd/...`.  Put 
`.nb-html2atom.yaml` into the same directory with binary file or into working 
dir (which should be a home dir, when run them with `newsbeuter`) and setup 
urls in your `urls` file.
