// HTML extraction selectively copied from Magpie internal/gateway/search_fetch.go
// (fetchText/htmlText/mdWriter) at commit
// 4534561f6bb8f8353a67801fbe864a6502517022. Compared with pi-web-search's
// regex extractor, the HTML parser also preserves links, lists, code, and
// basic table cells without leaking malformed script/comment bodies.
package webfetch

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// fetchText decodes a body from its declared or HTML-meta charset and returns
// its title and textual content.
func fetchText(body []byte, contentType, kind string, base *url.URL) [2]string {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		r = bytes.NewReader(body)
	}
	if kind == "text" {
		decoded, _ := io.ReadAll(r)
		return [2]string{"", strings.ToValidUTF8(string(decoded), "�")}
	}
	doc, err := html.Parse(r)
	if err != nil {
		return [2]string{"", ""}
	}
	return htmlText(doc, base)
}

func fetchClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// htmlText converts a document to Markdown-like text: headings, lists, links
// (made absolute), code, paragraphs, and basic table structure are retained;
// scripts, styles, hidden elements, and non-content markup are dropped.
func htmlText(doc *html.Node, base *url.URL) [2]string {
	m := &mdWriter{base: base}
	m.walk(doc)
	return [2]string{strings.Join(strings.Fields(m.title), " "), m.done()}
}

type mdWriter struct {
	b     bytes.Buffer
	base  *url.URL
	title string
	pre   int
	space bool
	tight bool // no space after a link's "[", list marker, or heading marker
	lists []int
}

var mdSkip = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"svg": true, "iframe": true, "object": true, "canvas": true,
	"head": true, "select": true, "button": true, "input": true,
	"textarea": true,
}

var mdBlock = map[string]bool{
	"p": true, "div": true, "section": true, "article": true,
	"header": true, "footer": true, "main": true, "aside": true,
	"nav": true, "table": true, "tr": true, "blockquote": true,
	"form": true, "figure": true, "figcaption": true, "dl": true,
	"dt": true, "dd": true, "address": true, "details": true,
	"summary": true, "body": true, "ul": true, "ol": true,
}

// newline ends the current line, with at most n blank-ish breaks in a row.
func (m *mdWriter) newline(n int) {
	m.tight = false
	s := m.b.Bytes()
	have := len(s) - len(bytes.TrimRight(s, "\n"))
	if len(s) == 0 {
		have = n
	}
	for ; have < n; have++ {
		m.b.WriteByte('\n')
	}
	m.space = false
}

func (m *mdWriter) atLineStart() bool {
	s := m.b.Bytes()
	return len(s) == 0 || s[len(s)-1] == '\n'
}

func (m *mdWriter) words(t string) {
	if m.pre > 0 {
		m.b.WriteString(t)
		return
	}
	lead := len(t) > 0 && strings.TrimLeft(t[:1], " \t\r\n\f") == ""
	trail := len(t) > 0 && strings.TrimRight(t[len(t)-1:], " \t\r\n\f") == ""
	fields := strings.Fields(t)
	if len(fields) == 0 {
		if lead {
			m.space = true
		}
		return
	}
	if (lead || m.space) && !m.atLineStart() && !m.tight {
		m.b.WriteByte(' ')
	}
	m.tight = false
	m.b.WriteString(strings.Join(fields, " "))
	m.space = trail
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func (m *mdWriter) kids(n *html.Node) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		m.walk(child)
	}
}

func (m *mdWriter) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		m.words(n.Data)
		return
	case html.DocumentNode:
		m.kids(n)
		return
	case html.ElementNode:
	default:
		return
	}
	tag := n.Data
	if tag == "title" && m.title == "" {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.TextNode {
				m.title += child.Data
			}
		}
		return
	}
	if tag == "head" {
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && child.Data == "title" {
				m.walk(child)
			}
		}
		return
	}
	if mdSkip[tag] || hasAttr(n, "hidden") || attr(n, "aria-hidden") == "true" {
		return
	}

	switch tag {
	case "br":
		m.b.WriteByte('\n')
		m.space = false
	case "hr":
		m.newline(2)
		m.b.WriteString("---")
		m.newline(2)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		m.newline(2)
		m.b.WriteString(strings.Repeat("#", int(tag[1]-'0')) + " ")
		m.tight = true
		m.kids(n)
		m.newline(2)
	case "li":
		m.newline(1)
		depth := max(len(m.lists)-1, 0)
		m.b.WriteString(strings.Repeat("  ", depth))
		if len(m.lists) > 0 && m.lists[len(m.lists)-1] > 0 {
			fmt.Fprintf(&m.b, "%d. ", m.lists[len(m.lists)-1])
			m.lists[len(m.lists)-1]++
		} else {
			m.b.WriteString("- ")
		}
		m.tight = true
		m.kids(n)
		m.newline(1)
	case "ul", "ol":
		start := 0
		if tag == "ol" {
			start = 1
		}
		m.lists = append(m.lists, start)
		m.newline(1)
		m.kids(n)
		m.lists = m.lists[:len(m.lists)-1]
		m.newline(1)
	case "pre":
		m.newline(2)
		m.b.WriteString("```\n")
		m.pre++
		m.kids(n)
		m.pre--
		m.newline(1)
		m.b.WriteString("```")
		m.newline(2)
	case "code":
		if m.pre > 0 {
			m.kids(n)
			return
		}
		if m.space && !m.atLineStart() {
			m.b.WriteByte(' ')
		}
		m.space = false
		m.b.WriteByte('`')
		m.kids(n)
		m.b.WriteByte('`')
	case "td", "th":
		if !m.atLineStart() {
			m.b.WriteString(" | ")
		}
		m.space = false
		m.kids(n)
	case "a":
		href := strings.TrimSpace(attr(n, "href"))
		link := ""
		if href != "" && !strings.HasPrefix(href, "#") && m.base != nil {
			if target, err := m.base.Parse(href); err == nil && (target.Scheme == "http" || target.Scheme == "https") {
				link = target.String()
			}
		}
		if link == "" || m.pre > 0 {
			m.kids(n)
			return
		}
		if m.space && !m.atLineStart() {
			m.b.WriteByte(' ')
		}
		m.space = false
		at := m.b.Len()
		m.b.WriteByte('[')
		m.tight = true
		m.kids(n)
		m.tight = false
		if m.b.Len() == at+1 {
			m.b.Truncate(at)
			return
		}
		m.b.WriteString("](" + link + ")")
	case "img":
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			m.words(" " + alt + " ")
		}
	default:
		if mdBlock[tag] {
			m.newline(2)
			if tag == "blockquote" {
				m.b.WriteString("> ")
			}
			m.kids(n)
			m.newline(2)
			return
		}
		m.kids(n)
	}
}

func (m *mdWriter) done() string {
	lines := strings.Split(m.b.String(), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if trimmed := strings.TrimSpace(line); trimmed == "-" || trimmed == ">" {
			line = ""
		}
		if line == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
