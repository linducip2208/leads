// Package fixsrv is a deterministic local fixture website farm for crawler,
// pipeline and scale tests. No external network required.
//
// Endpoints (all under one server; company identity derives from path):
//
//	/co/{slug}        static company page (contact info varies by slug)
//	/co/{slug}/about  about page linking back + to /contact
//	/co/{slug}/contact contact page with email/phone/wa links
//	/js                JS shell (mount node, almost no content)
//	/redirect          301 → /co/acme
//	/missing           404
//	/flaky             429 twice, then 200
//	/slow              2s delayed 200
package fixsrv

import (
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Server is a fixture farm.
type Server struct {
	srv                *http.Server
	URL                string
	flaky              atomic.Int64
	serviceUnavailable atomic.Int64
}

// New starts the farm on 127.0.0.1:0 and returns it.
func New() (*Server, error) {
	f := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/co/", f.company)
	mux.HandleFunc("/js", f.jsShell)
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/co/acme", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/flaky", f.flakyHandler)
	mux.HandleFunc("/unavailable", f.unavailableHandler)
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /robots-deny\n")
	})
	mux.HandleFunc("/robots-deny", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><h1>Should not be fetched</h1></body></html>`)
	})
	mux.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		z := gzip.NewWriter(w)
		_, _ = z.Write([]byte(`<html><head><title>Gzip Co</title></head><body><a href="mailto:hello@gzip.co.id">hello@gzip.co.id</a></body></html>`))
		_ = z.Close()
	})
	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Large</h1>`))
		_, _ = w.Write([]byte(strings.Repeat("x", 6<<20)))
		_, _ = w.Write([]byte(`</body></html>`))
	})
	mux.HandleFunc("/invalid", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte{0xff, 0xfe, '<', 'h', 't', 'm', 'l', '>'})
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-waitChan(2000):
		}
		fmt.Fprint(w, "<html><body><h1>Slow Co</h1></body></html>")
	})
	f.srv = &http.Server{Handler: mux}
	ln, err := listenLocal()
	if err != nil {
		return nil, err
	}
	f.URL = "http://" + ln.Addr().String()
	go f.srv.Serve(ln) //nolint:errcheck
	return f, nil
}

// Close stops the farm.
func (f *Server) Close() { _ = f.srv.Close() }

func (f *Server) company(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/co/")
	parts := strings.SplitN(rest, "/", 2)
	slug := parts[0]
	page := ""
	if len(parts) > 1 {
		page = parts[1]
	}
	name := "PT " + titleize(slug) + " Sejahtera"
	domain := slug + ".co.id"
	switch page {
	case "", "/":
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>%s</title>
<meta name="description" content="Perusahaan %s di Jakarta.">
<meta property="og:site_name" content="%s"></head><body>
<h1>%s</h1>
<p>Email <a href="mailto:info@%s">info@%s</a>, telp 0812-000-%s.</p>
<a href="/co/%s/about">Tentang</a> <a href="/co/%s/contact">Kontak</a>
<a href="https://wa.me/62812000%s">WA</a>
<script src="/wp-content/x.js"></script>
</body></html>`, name, name, name, name, domain, domain, digits(slug), slug, slug, digits(slug))
	case "about":
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Tentang %s</title></head><body>
<h1>Tentang Kami</h1><p>%s adalah perusahaan jasa.</p>
<address>Jl. Merdeka No. %s, Jakarta</address>
<a href="/co/%s/contact">Kontak</a></body></html>`, name, name, digits(slug), slug)
	case "contact":
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Kontak %s</title></head><body>
<h1>Kontak</h1><p><a href="mailto:cs@%s">cs@%s</a> | (021) 555-%s</p>
</body></html>`, name, domain, domain, digits(slug))
	default:
		http.NotFound(w, r)
	}
}

func (f *Server) jsShell(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, `<!DOCTYPE html><html><head><title>App</title></head><body>
<div id="root"></div><div id="app"></div>
<script src="/static/js/app.js"></script>
<noscript>You need to enable JavaScript to run this app.</noscript>
</body></html>`)
}

func (f *Server) flakyHandler(w http.ResponseWriter, r *http.Request) {
	n := f.flaky.Add(1)
	if n <= 2 {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	fmt.Fprint(w, `<html><body><h1>Flaky Co</h1><p><a href="mailto:hi@flaky.co.id">hi@flaky.co.id</a></p></body></html>`)
}

func (f *Server) unavailableHandler(w http.ResponseWriter, r *http.Request) {
	if f.serviceUnavailable.Add(1) <= 2 {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	f.writeCompanyPage(w, "Unavailable Co", "hello@unavailable.co.id")
}

func (f *Server) writeCompanyPage(w http.ResponseWriter, name, email string) {
	fmt.Fprintf(w, `<html><head><title>%s</title></head><body><h1>%s</h1><a href="mailto:%s">%s</a></body></html>`, name, name, email, email)
}

func titleize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func listenLocal() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

func waitChan(ms int) <-chan time.Time {
	return time.After(time.Duration(ms) * time.Millisecond)
}

func digits(s string) string {
	sum := 0
	for _, r := range s {
		sum += int(r)
	}
	return fmt.Sprintf("%04d", sum%10000)
}
