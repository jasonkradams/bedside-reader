package main

import (
	"log"
	"net"
	"net/http"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
)

func main() {
	log.Println("Starting Bedside Audiobook Appliance...")

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := `<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Bedside</title>
	<style>
		body {
			margin: 0;
			padding: 0;
			background-color: #000;
			color: #fff;
			font-family: 'Inter', sans-serif;
			display: flex;
			align-items: center;
			justify-content: center;
			height: 100vh;
			width: 100vw;
		}
		h1 {
			font-size: 24px;
			font-weight: 300;
		}
	</style>
</head>
<body>
	<h1>Bedside Reader Online</h1>
</body>
</html>
`
		w.Write([]byte(html))
	})

	server := &http.Server{
		Addr:         ":8080",
		Handler:      nil,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Println("Listening on :8080...")

	// Notify systemd that the service is ready
	sent, err := daemon.SdNotify(false, daemon.SdNotifyReady)
	if err != nil {
		log.Printf("Failed to notify systemd: %v", err)
	} else if sent {
		log.Println("Systemd notified of readiness.")
	}

	if err := server.Serve(ln); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
