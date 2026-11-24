package main

import (
	"embed"
	"flag"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
)

//go:embed site
var site embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:7360", "address to serve the app on")
	other := flag.String("other", "", "second origin (cross-origin iframe tests); default localhost on the -addr port + 1; -other= to skip")
	clickLog := flag.String("clicklog", "", "append the click instrument's records (?instrument=1) to this file")
	flag.Parse()
	otherSet := false
	flag.Visit(func(f *flag.Flag) { otherSet = otherSet || f.Name == "other" })
	if !otherSet {
		*other = nextPort(*addr)
	}
	root, _ := fs.Sub(site, "site")
	h := http.FileServer(http.FS(root))
	noCache := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
	var logMu sync.Mutex
	http.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if *clickLog != "" && r.Method == http.MethodPost && len(body) > 0 {
			logMu.Lock()
			if f, err := os.OpenFile(*clickLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
				f.Write(append(body, '\n'))
				f.Close()
			}
			logMu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	http.Handle("/", noCache)
	if *other != "" {
		go func() { log.Println(http.ListenAndServe(*other, noCache)) }()
	}
	log.Printf("Buddy Bench (web) on http://%s/", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

func nextPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	n, err2 := strconv.Atoi(port)
	if err != nil || err2 != nil {
		log.Fatalf("-addr %q: want host:port", addr)
	}
	return net.JoinHostPort("localhost", strconv.Itoa(n+1))
}
