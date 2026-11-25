// Command serve serves the static page.
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8000", "listen address")
	dir := flag.String("dir", "dist", "page directory (the output of build.sh)")
	flag.Parse()
	http.Handle("/", http.FileServer(http.Dir(*dir)))
	log.Printf("serving %s on http://%s/", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
