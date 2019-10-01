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
	"strings"
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
	debug        bool
	port         uint
	envoyNodeIDs string
)

const grpcMaxConcurrentStreams = 1000000

func init() {
	flag.BoolVar(&debug, "debug", true, "Use debug logging")
	flag.UintVar(&port, "port", 18000, "Management server port")
	flag.StringVar(&envoyNodeIDs, "nodeIDs", "envoy_1,envoy_2", "comma-separated list of envoy node ids")
}

// main returns code 1 if any of the batches failed to pass all requests
func main() {
	flag.Parse()

	nodeIDs := strings.Split(envoyNodeIDs, ",")

	// create a cache
	sigs := make(chan struct{})
	cb := &callbacks{signal: sigs}

	// config holds all the latest discovery information
	config := cache.NewSnapshotCache(mode == resource.Ads, cache.IDHash{}, logger{})

	// srv links the config and callbacks
	srv := server.NewServer(config, cb)

	ctx, cancel := context.WithCancel(context.Background())
	go runManagementServer(ctx, srv, port)

	// do discovery every 15 seconds
	// changes (if any) in the hashed resources will prompt an update
	ticker := time.NewTicker(15 * time.Second)
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				snapshot := Discover()
				log.Printf("validating snapshot %+v\n", snapshot)
				if err := snapshot.Consistent(); err != nil {
					log.Printf("snapshot inconsistency: %+v\n", snapshot)
				}
				// populate the config for each envoy-node-id
				for _, n := range nodeIDs {
					id := strings.TrimSpace(n)
					log.Printf("setting snapshot for node %s", id)
					err := config.SetSnapshot(id, snapshot)
					if err != nil {
						log.Printf("snapshot error %q for %+v\n", err, snapshot)
					}
				}
			}
		}
	}()

	// wait for signal to exit
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-osSignals
		ticker.Stop()
		done <- true
		cancel()
	}()
	select {
	case <-ctx.Done():
		log.Printf("exiting after cancellation")
		os.Exit(0)
	}
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
