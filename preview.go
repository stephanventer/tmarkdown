package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	chstyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// previewServer serves the document as HTML on loopback and pushes reload
// and scroll events to open pages over Server-Sent Events. Raw HTML in the
// markdown is omitted, and files are only served from the document's
// directory, so a document can't reach outside it.
type previewServer struct {
	url   string
	srv   *http.Server
	root  *os.Root // document directory; nil for stdin
	style string
	md    goldmark.Markdown

	mu      sync.Mutex
	body    []byte // rendered HTML fragment
	scroll  string // last scroll event, JSON
	clients map[chan string]struct{}
}

func startPreview(src []byte, docPath, style string) (*previewServer, error) {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	prefix := "/p/" + hex.EncodeToString(tok) + "/"
	p := &previewServer{
		url:     "http://" + ln.Addr().String() + prefix,
		style:   style,
		md:      newPreviewMarkdown(style),
		clients: map[chan string]struct{}{},
	}
	if docPath != "" {
		if r, err := os.OpenRoot(filepath.Dir(docPath)); err == nil {
			p.root = r
		}
	}
	p.SetSource(src)

	mux := http.NewServeMux()
	mux.Handle(prefix, http.StripPrefix(strings.TrimSuffix(prefix, "/"), http.HandlerFunc(p.handle)))
	p.srv = &http.Server{Handler: mux}
	go p.srv.Serve(ln) //nolint:errcheck
	return p, nil
}

// Close stops the server and ends any open event streams.
func (p *previewServer) Close() {
	if p == nil {
		return
	}
	p.srv.Close()
	if p.root != nil {
		p.root.Close()
	}
}

// SetSource re-renders the document and tells open pages to fetch it.
func (p *previewServer) SetSource(src []byte) {
	var buf bytes.Buffer
	if err := p.md.Convert(src, &buf); err != nil {
		buf.Reset()
		buf.WriteString("<pre>" + template.HTMLEscapeString(err.Error()) + "</pre>")
	}
	p.mu.Lock()
	changed := !bytes.Equal(buf.Bytes(), p.body)
	p.body = buf.Bytes()
	p.mu.Unlock()
	if changed {
		p.broadcast("event: reload\ndata: {}\n\n")
	}
}

// Scroll asks open pages to show the n-th heading (0-based) with this text.
// Repeated calls for the same heading are ignored.
func (p *previewServer) Scroll(text string, n int) {
	b, _ := json.Marshal(struct {
		Text string `json:"text"`
		N    int    `json:"n"`
	}{text, n})
	msg := "event: scroll\ndata: " + string(b) + "\n\n"
	p.mu.Lock()
	same := msg == p.scroll
	p.scroll = msg
	p.mu.Unlock()
	if !same {
		p.broadcast(msg)
	}
}

func (p *previewServer) broadcast(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.clients {
		select {
		case c <- msg:
		default: // slow client: drop rather than block the viewer
		}
	}
}

func (p *previewServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch r.URL.Path {
	case "/", "":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src * data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		p.mu.Lock()
		body := p.body
		p.mu.Unlock()
		pageTmpl.Execute(w, struct { //nolint:errcheck
			CSS  template.CSS
			Body template.HTML
		}{template.CSS(previewCSS(p.style)), template.HTML(body)})
	case "/_/content":
		p.mu.Lock()
		body := p.body
		p.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(body) //nolint:errcheck
	case "/_/events":
		p.events(w, r)
	default:
		p.serveFile(w, r)
	}
}

func (p *previewServer) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	c := make(chan string, 8)
	p.mu.Lock()
	p.clients[c] = struct{}{}
	last := p.scroll
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.clients, c)
		p.mu.Unlock()
	}()
	io.WriteString(w, ": hi\n\n") //nolint:errcheck
	if last != "" {
		io.WriteString(w, last) //nolint:errcheck
	}
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c:
			if _, err := io.WriteString(w, msg); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// serveFile serves images and other assets relative to the document.
// os.Root refuses paths that escape the directory, including via symlinks.
func (p *previewServer) serveFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p.root == nil || name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
		http.NotFound(w, r)
		return
	}
	f, err := p.root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, name, st.ModTime(), f)
}

// ---- markdown to HTML ----

func newPreviewMarkdown(style string) goldmark.Markdown {
	cs := "github"
	if style != "light" {
		cs = "github-dark"
	}
	return goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(&codeRenderer{style: chstyles.Get(cs)}, 100)),
		),
	)
}

// codeRenderer highlights fenced code blocks with chroma, using inline
// styles so the page needs no extra stylesheet.
type codeRenderer struct{ style *chroma.Style }

func (c *codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, c.render)
}

func (c *codeRenderer) render(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	fb := n.(*ast.FencedCodeBlock)
	var code bytes.Buffer
	for i := 0; i < fb.Lines().Len(); i++ {
		seg := fb.Lines().At(i)
		code.Write(seg.Value(src))
	}
	lang := string(fb.Language(src))
	lx := lexers.Get(lang)
	if lx == nil {
		lx = lexers.Fallback
	}
	it, err := chroma.Coalesce(lx).Tokenise(nil, code.String())
	if err == nil {
		err = chtml.New(chtml.WithClasses(false), chtml.PreventSurroundingPre(false)).Format(w, c.style, it)
	}
	if err != nil {
		w.WriteString("<pre><code>")
		ghtml.DefaultWriter.Write(w, code.Bytes())
		w.WriteString("</code></pre>")
	}
	return ast.WalkSkipChildren, nil
}

// ---- page ----

func previewCSS(style string) string {
	p := paletteFor(style)
	return fmt.Sprintf(`
:root { color-scheme: %s; }
html { background: %s; }
body { color: %s; max-width: 860px; margin: 0 auto; padding: 2rem 1.5rem 50vh;
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; }
h1, h2, h3, h4, h5, h6 { line-height: 1.25; margin: 1.6em 0 .6em; scroll-margin-top: 1rem; }
h1 { color: %s; font-size: 2em; border-bottom: 2px solid %s; padding-bottom: .3em; }
h2 { color: %s; font-size: 1.5em; border-bottom: 1px solid %s; padding-bottom: .3em; }
h3 { color: %s; font-size: 1.25em; }
h4, h5, h6 { color: %s; }
strong { color: %s; }
a { color: %s; }
code { background: %s; padding: .15em .35em; border-radius: 4px; font-size: .9em;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
pre { padding: 1em; border-radius: 6px; overflow-x: auto; background: %s !important; }
pre code { background: none; padding: 0; }
blockquote { margin: 0; padding: 0 1em; color: %s; border-left: 3px solid %s; }
hr { border: 0; border-top: 1px solid %s; }
table { border-collapse: collapse; }
th, td { border: 1px solid %s; padding: .4em .8em; }
th { color: %s; }
img { max-width: 100%%; }
li::marker { color: %s; }
`, map[bool]string{true: "light", false: "dark"}[style == "light"],
		pickBg(style), p.text, p.bright, p.green, p.bright, p.faint, p.green, p.soft, p.pale, p.green,
		p.codeBg, p.codeBg, p.muted, p.muted, p.faint, p.faint, p.bright, p.muted)
}

// pickBg is the page background. The terminal palette has no page colour
// (the terminal supplies it), so use a near-black or near-white.
func pickBg(style string) string {
	if style == "light" {
		return "#F7FBF7"
	}
	return "#050A05"
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>tmarkdown preview</title><style>{{.CSS}}</style></head>
<body><main id="content">{{.Body}}</main>
<script>
(function () {
  var main = document.getElementById('content');
  function find(t, n) {
    var hs = main.querySelectorAll('h1,h2,h3,h4,h5,h6'), k = 0;
    for (var i = 0; i < hs.length; i++) {
      if (hs[i].textContent.trim() === t) { if (k === n) return hs[i]; k++; }
    }
    return null;
  }
  var es = new EventSource('_/events');
  es.addEventListener('reload', function () {
    fetch('_/content', {cache: 'no-store'}).then(function (r) { return r.text(); })
      .then(function (html) { main.innerHTML = html; });
  });
  es.addEventListener('scroll', function (e) {
    var d = JSON.parse(e.data), h = find(d.text, d.n);
    if (h) h.scrollIntoView({behavior: 'smooth', block: 'start'});
  });
})();
</script></body></html>
`))
