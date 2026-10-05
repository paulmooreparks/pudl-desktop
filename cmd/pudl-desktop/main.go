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
	"time"

	"github.com/paulmooreparks/pudl-desktop/internal/store"
	"github.com/paulmooreparks/pudl-desktop/internal/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8200", "the address to listen on")
	data := flag.String("data", "data", "the folder that holds the database and the documents")
	secure := flag.Bool("secure", false, "mark cookies Secure, for serving behind HTTPS")
	runHost := flag.String("run-host", "", "the host of the separate origin where code built in PS runs")
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
	srv.RunHost = *runHost

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
