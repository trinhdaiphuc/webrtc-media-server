package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/trinhdaiphuc/webrtc-media-server/internal/controller"
	"github.com/trinhdaiphuc/webrtc-media-server/pkg/log"
	"github.com/trinhdaiphuc/webrtc-media-server/web"
)

var (
	addr = flag.String("addr", ":8080", "http service address")
	ll   = log.New()
)

func init() {
	flag.Parse()
}

func main() {
	mux := http.NewServeMux()
	mux.Handle("/", web.Handler())
	mux.HandleFunc("/websocket", controller.WSHandler)
	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		IdleTimeout:       5 * time.Minute,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// start HTTP server
	ll.Info("Starting http server", log.String("port", *addr))
	go func() {
		err := server.ListenAndServe()
		if err != nil {
			return
		}
	}()

	// request a keyframe every 3 seconds
	go func() {
		for range time.NewTicker(time.Second * 3).C {
			controller.DispatchKeyFrame()
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	<-stop

	ll.Info("Stopping server...")
	err := server.Shutdown(context.Background())
	if err != nil {
		ll.Error("Error shutting down server", log.Error(err))
		return
	}
	ll.Info("Stop server successfully")
}
