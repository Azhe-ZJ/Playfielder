package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Azhe-ZJ/Playfielder/internal/agent"
	"github.com/Azhe-ZJ/Playfielder/internal/httpapi"
	"github.com/Azhe-ZJ/Playfielder/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	addr := flag.String("addr", "127.0.0.1:4177", "Loopback listen address")
	data := flag.String("data", "./data", "Independent data directory")
	flag.Parse()
	if !httpapi.AllowedAddress(*addr) {
		return fmt.Errorf("this edition accepts loopback addresses only; hosted deployment needs a separate access-control design")
	}
	provider := &agent.Remote{BaseURL: os.Getenv("PLAYFIELDER_MODEL_BASE_URL"), APIKey: os.Getenv("PLAYFIELDER_MODEL_API_KEY"), Model: os.Getenv("PLAYFIELDER_MODEL"), Mode: os.Getenv("PLAYFIELDER_MODEL_MODE")}
	if e := provider.Validate(); e != nil {
		return e
	}
	s, e := store.Open(*data)
	if e != nil {
		return e
	}
	defer s.Close()
	engine, e := agent.New(s, provider)
	if e != nil {
		return e
	}
	defer engine.Close()
	listener, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	server := &http.Server{Handler: httpapi.New(s, engine), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Printf("Playfielder 0.1.0\nLocal workspace: http://%s\nData directory: %s\nAI configured: %t\n", listener.Addr(), *data, provider.Configured())
	e = server.Serve(listener)
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
