package main

import (
	"log"
)

func main() {
	log.Println("Distributed Rate Limiter Service")
	// TODO:
	// 1. Initialize config rule watcher
	// 2. Connect to Redis Cluster and load Lua scripts
	// 3. Start Prometheus /metrics and /healthz HTTP server
	// 4. Start gRPC server and listen for Allow RPCs
}
