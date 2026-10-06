// Copyright 2026 Paul Parks. SPDX-License-Identifier: Apache-2.0

// Command pudl-desktop serves PUDL Desktop.
//
//	pudl-desktop -addr 127.0.0.1:8200 -data ./data
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/paulmooreparks/pudl-desktop/internal/render"
	"github.com/paulmooreparks/pudl-desktop/internal/store"
	"github.com/paulmooreparks/pudl-desktop/internal/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8200", "the address to listen on")
	data := flag.String("data", "data", "the folder that holds the database and the documents")
	secure := flag.Bool("secure", false, "mark cookies Secure, for serving behind HTTPS")
	runHosts := flag.String("run-hosts", "", "comma-separated hosts of the separate origin where what PUDL Studio builds runs")
	runOrigin := flag.String("run-origin", "", "the run origin's address as a browser reaches it, such as https://pudl-run.example.com")
	renderBase := flag.String("render-base", "", "the run origin's address as the service's own browser reaches it, such as http://127.0.0.1:8200; empty draws no pictures")
	chrome := flag.String("chrome", "", "the Chromium browser that draws pictures; empty finds one")
	flag.Parse()

	st, err := store.Open(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	srv, err := web.New(st)
	if err != nil {
		log.Fatal(err)
	}
	srv.Secure = *secure
	for _, h := range strings.Split(*runHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			srv.RunHosts = append(srv.RunHosts, h)
		}
	}
	srv.RunOrigin = strings.TrimRight(*runOrigin, "/")
	if *renderBase != "" {
		srv.RenderBase = strings.TrimRight(*renderBase, "/")
		srv.Renderer = &render.Renderer{Path: *chrome}
		defer srv.Renderer.Close()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Expired workspaces, and the bytes of saves that never committed,
	// are swept every ten minutes.
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			if err := st.Sweep(ctx); err != nil {
				log.Printf("sweep: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	hs := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes: 1 << 16}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hs.Shutdown(shut)
	}()
	log.Printf("PUDL Desktop on http://%s", *addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
