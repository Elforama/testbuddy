// Command web serves Buddy Bench (web) on 127.0.0.1:7360, and a second origin on
// localhost:7361 for cross-origin iframe tests. Usage: go run ./bench/web
package main

import (
	"embed"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sync"
)

//go:embed site
var site embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:7360", "address to serve the app on")
	other := flag.String("other", "localhost:7361", "second origin (cross-origin iframe tests); empty to skip")
	clickLog := flag.String("clicklog", "", "append the click instrument's records (?instrument=1) to this file")
	flag.Parse()
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
