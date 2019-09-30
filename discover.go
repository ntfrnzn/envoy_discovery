package main

import (
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache"
	"github.com/golang/protobuf/ptypes"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/api/v2/endpoint"
)

const (
	EdsCluster = "eds_cluster"
)

func Discover() cache.Snapshot {
	// return cache.Snapshot{}

	clusterName := "k8s_api"
	host := "10.0.0.200"
	var port uint32 = 57116

	// version := time.Now().Format(time.RFC3339)
	version := "v1"
	endpoints := []cache.Resource{MakeEndpoint(clusterName, host, port)}
	clusters := []cache.Resource{MakeCluster(clusterName)}
	routes := []cache.Resource{}
	listeners := []cache.Resource{}
	return cache.NewSnapshot(
		version,
		endpoints,
		clusters,
		routes,
		listeners,
	)
}

// MakeEndpoint creates an endpoint on a given host and port.
func MakeEndpoint(clusterName, host string, port uint32) *v2.ClusterLoadAssignment {
	return &v2.ClusterLoadAssignment{
		ClusterName: clusterName,
		Endpoints: []*endpoint.LocalityLbEndpoints{{
			LbEndpoints: []*endpoint.LbEndpoint{{
				HostIdentifier: &endpoint.LbEndpoint_Endpoint{
					Endpoint: &endpoint.Endpoint{
						Address: &core.Address{
							Address: &core.Address_SocketAddress{
								SocketAddress: &core.SocketAddress{
									Protocol: core.SocketAddress_TCP,
									Address:  host,
									PortSpecifier: &core.SocketAddress_PortValue{
										PortValue: port,
									},
								},
							},
						},
					},
				},
			}},
		}},
	}
}

// MakeCluster creates a cluster using EDS.
func MakeCluster(clusterName string) *v2.Cluster {

	edsSource := &core.ConfigSource{
		ConfigSourceSpecifier: &core.ConfigSource_ApiConfigSource{
			ApiConfigSource: &core.ApiConfigSource{
				ApiType:                   core.ApiConfigSource_GRPC,
				SetNodeOnFirstMessageOnly: true,
				GrpcServices: []*core.GrpcService{{
					TargetSpecifier: &core.GrpcService_EnvoyGrpc_{
						EnvoyGrpc: &core.GrpcService_EnvoyGrpc{ClusterName: EdsCluster},
					},
				}},
			},
		},
	}

	connectTimeout := 500 * time.Millisecond
	return &v2.Cluster{
		Name:                 clusterName,
		ConnectTimeout:       ptypes.DurationProto(connectTimeout),
		ClusterDiscoveryType: &v2.Cluster_Type{Type: v2.Cluster_EDS},
		EdsClusterConfig: &v2.Cluster_EdsClusterConfig{
			EdsConfig: edsSource,
		},
	}
}
