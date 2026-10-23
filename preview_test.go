package main

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func previewFixture(t *testing.T) (*previewServer, string) {
	t.Helper()
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.md")
	src := "# Title\n\n<script>alert(1)</script>\n\n```go\nfunc main() {}\n```\n\n![pic](img.png)\n"
	if err := os.WriteFile(doc, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "img.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := startPreview([]byte(src), doc, "dark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, dir
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestPreviewPage(t *testing.T) {
	p, _ := previewFixture(t)
	if !strings.HasPrefix(p.url, "http://127.0.0.1:") {
		t.Fatalf("not loopback: %s", p.url)
	}
	code, body := get(t, p.url)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{`<h1 id="title">Title</h1>`, "#00FF41", `style="`} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "alert(1)") {
		t.Error("raw HTML was not omitted")
	}
}

func TestPreviewNeedsToken(t *testing.T) {
	p, _ := previewFixture(t)
	base := p.url[:strings.Index(p.url, "/p/")]
	if code, _ := get(t, base+"/"); code != 404 {
		t.Errorf("root without token: status %d", code)
	}
	if code, _ := get(t, base+"/p/wrong/"); code != 404 {
		t.Errorf("wrong token: status %d", code)
	}
}

func TestPreviewFiles(t *testing.T) {
	p, _ := previewFixture(t)
	if code, body := get(t, p.url+"img.png"); code != 200 || body != "png" {
		t.Errorf("image: %d %q", code, body)
	}
	for _, bad := range []string{"../secret.txt", "..%2fsecret.txt", "%2e%2e/secret.txt", ".hidden"} {
		if code, body := get(t, p.url+bad); code == 200 || strings.Contains(body, "secret") {
			t.Errorf("%s: served (%d)", bad, code)
		}
	}
}

func TestPreviewEvents(t *testing.T) {
	p, _ := previewFixture(t)
	resp, err := http.Get(p.url + "_/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	waitFor := func(want string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case l := <-lines:
				if l == want {
					return
				}
			case <-deadline:
				t.Fatalf("no %q", want)
			}
		}
	}
	waitFor(": hi")
	p.SetSource([]byte("# Changed\n"))
	waitFor("event: reload")
	p.Scroll("Changed", 0)
	waitFor(`data: {"text":"Changed","n":0}`)

	if _, body := get(t, p.url+"_/content"); !strings.Contains(body, "Changed") {
		t.Errorf("content not updated: %q", body)
	}
}
