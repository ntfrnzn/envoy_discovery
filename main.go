// Copyright 2018 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

// Package main contains the test driver for testing xDS manually.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v2"
	"github.com/envoyproxy/go-control-plane/pkg/cache"
	"github.com/envoyproxy/go-control-plane/pkg/server"
	xds "github.com/envoyproxy/go-control-plane/pkg/server"
	"github.com/envoyproxy/go-control-plane/pkg/test/resource"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	debug bool

	port         uint
	gatewayPort  uint
	upstreamPort uint
	basePort     uint
	alsPort      uint

	delay    time.Duration
	requests int
	updates  int

	mode          string
	clusters      int
	httpListeners int
	tcpListeners  int
	runtimes      int
	tls           bool

	nodeID string
)

const grpcMaxConcurrentStreams = 1000000

func init() {
	flag.BoolVar(&debug, "debug", true, "Use debug logging")
	flag.UintVar(&port, "port", 18000, "Management server port")
	flag.UintVar(&gatewayPort, "gateway", 18001, "Management server port for HTTP gateway")
	// flag.UintVar(&upstreamPort, "upstream", 18080, "Upstream HTTP/1.1 port")
	// flag.UintVar(&basePort, "base", 9000, "Listener port")
	// flag.UintVar(&alsPort, "als", 18090, "Accesslog server port")
	// flag.DurationVar(&delay, "delay", 500*time.Millisecond, "Interval between request batch retries")
	// flag.IntVar(&requests, "r", 5, "Number of requests between snapshot updates")
	// flag.IntVar(&updates, "u", 3, "Number of snapshot updates")
	// flag.StringVar(&mode, "xds", resource.Ads, "Management server type (ads, xds, rest)")
	// flag.IntVar(&clusters, "clusters", 4, "Number of clusters")
	// flag.IntVar(&httpListeners, "http", 2, "Number of HTTP listeners (and RDS configs)")
	// flag.IntVar(&tcpListeners, "tcp", 2, "Number of TCP pass-through listeners")
	// flag.IntVar(&runtimes, "runtimes", 1, "Number of RTDS layers")
	// flag.StringVar(&nodeID, "nodeID", "test-id", "Node ID")
	// flag.BoolVar(&tls, "tls", false, "Enable TLS on all listeners and use SDS for secret delivery")
}

// runManagementServer starts an xDS server at the given port.
func runManagementServer(ctx context.Context, server xds.Server, port uint) {
	// gRPC golang library sets a very small upper bound for the number gRPC/h2
	// streams over a single TCP connection. If a proxy multiplexes requests over
	// a single connection to the management server, then it might lead to
	// availability problems.
	var grpcOptions []grpc.ServerOption
	grpcOptions = append(grpcOptions, grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams))
	grpcServer := grpc.NewServer(grpcOptions...)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatal(err)
	}

	// register services
	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(grpcServer, server)
	v2.RegisterEndpointDiscoveryServiceServer(grpcServer, server)
	v2.RegisterClusterDiscoveryServiceServer(grpcServer, server)
	v2.RegisterRouteDiscoveryServiceServer(grpcServer, server)
	v2.RegisterListenerDiscoveryServiceServer(grpcServer, server)
	discoverygrpc.RegisterSecretDiscoveryServiceServer(grpcServer, server)

	// Register reflection service on gRPC server.
	reflection.Register(grpcServer)

	log.Printf("management server listening on %d\n", port)
	go func() {
		if err = grpcServer.Serve(lis); err != nil {
			log.Println(err)
		}
	}()
	<-ctx.Done()

	grpcServer.GracefulStop()
}

// main returns code 1 if any of the batches failed to pass all requests
func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())

	// start upstream
	// go test.RunHTTP(ctx, upstreamPort)

	// create a cache
	sigs := make(chan struct{})

	cb := &callbacks{signal: sigs}

	// config holds all the latest discovery information
	config := cache.NewSnapshotCache(mode == resource.Ads, cache.IDHash{}, logger{})

	// srv links the config and callbacks
	srv := server.NewServer(config, cb)

	go runManagementServer(ctx, srv, port)

	snapshot := Discover()
	log.Printf("validating snapshot %+v\n", snapshot)
	if err := snapshot.Consistent(); err != nil {
		log.Printf("snapshot inconsistency: %+v\n", snapshot)
	}

	err := config.SetSnapshot(nodeID, snapshot)
	if err != nil {
		log.Printf("snapshot error %q for %+v\n", err, snapshot)
		os.Exit(1)
	}

	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-osSignals
		cancel()
	}()

	select {
	case <-ctx.Done():
		log.Printf("exiting after cancellation")
		os.Exit(0)
	}
}

type logger struct{}

func (logger logger) Infof(format string, args ...interface{}) {
	if debug {
		log.Printf(format+"\n", args...)
	}
}
func (logger logger) Errorf(format string, args ...interface{}) {
	log.Printf(format+"\n", args...)
}

type callbacks struct {
	signal   chan struct{}
	fetches  int
	requests int
	mu       sync.Mutex
}

func (cb *callbacks) Report() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	log.Printf("server callbacks fetches=%d requests=%d\n", cb.fetches, cb.requests)
}
func (cb *callbacks) OnStreamOpen(_ context.Context, id int64, typ string) error {
	if debug {
		log.Printf("stream %d open for type %s\n", id, typ)
	}
	return nil
}
func (cb *callbacks) OnStreamClosed(id int64) {
	if debug {
		log.Printf("stream %d closed\n", id)
	}
}
func (cb *callbacks) OnStreamRequest(id int64, req *v2.DiscoveryRequest) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.requests++
	if cb.signal != nil {
		close(cb.signal)
		cb.signal = nil
	}
	if debug {
		log.Printf("OnStreamRequest: %+v\n", req)
	}
	return nil
}
func (cb *callbacks) OnStreamResponse(id int64, req *v2.DiscoveryRequest, resp *v2.DiscoveryResponse) {
	if debug {
		log.Printf("OnStreamResponse: %+v\n", resp)
	}
}
func (cb *callbacks) OnFetchRequest(_ context.Context, req *v2.DiscoveryRequest) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.fetches++
	if cb.signal != nil {
		close(cb.signal)
		cb.signal = nil
	}
	if debug {
		log.Printf("OnFetchRequest: %+v\n", req)
	}
	return nil
}
func (cb *callbacks) OnFetchResponse(*v2.DiscoveryRequest, *v2.DiscoveryResponse) {
	if debug {
		log.Printf("OnFetchResponse")
	}
}
