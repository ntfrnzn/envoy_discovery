package main

import (
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/golang/protobuf/ptypes"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/api/v2/endpoint"
	listener "github.com/envoyproxy/go-control-plane/envoy/api/v2/listener"
	tcp "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/tcp_proxy/v2"
	"github.com/golang/protobuf/ptypes/any"
)

const (
	XdsCluster = "xds_cluster"
)

func Discover() cache.Snapshot {
	// return cache.Snapshot{}

	clusterName := "vYeiGyJc9Z88"
	sniHost := "k8s.vYeiGyJc9Z88.ntfrnzn.dev"

	endpointHHost := "10.0.0.200"
	var endpointPort uint32 = 62953

	listenerHost := "0.0.0.0"
	listenerName := "k8s-proxy"
	var listenerPort uint32 = 6443

	version := time.Now().Format(time.RFC3339)
	//version := "0"

	endpoints := []cache.Resource{MakeEndpoint(clusterName, endpointHHost, endpointPort)}
	clusters := []cache.Resource{MakeCluster(clusterName)}
	routes := []cache.Resource{}

	tcpListener := MakeTCPListener(listenerName, listenerHost, listenerPort)
	err := AddFilterChain(clusterName, sniHost, tcpListener)
	if err != nil {
		panic(err)
	}
	listeners := []cache.Resource{tcpListener}

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
						EnvoyGrpc: &core.GrpcService_EnvoyGrpc{ClusterName: XdsCluster},
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

// AddFilterChain appends a cluser and SNI hostname to the TcpListener
func AddFilterChain(clusterName, sniHost string, tcpListener *v2.Listener) error {

	// TCP filter configuration
	config := &tcp.TcpProxy{
		StatPrefix: "tcp",
		ClusterSpecifier: &tcp.TcpProxy_Cluster{
			Cluster: clusterName,
		},
	}
	pbst, err := ptypes.MarshalAny(config)
	if err != nil {
		return err
	}
	tcpListener.FilterChains = append(
		tcpListener.FilterChains,
		&listener.FilterChain{
			FilterChainMatch: &listener.FilterChainMatch{
				ServerNames: []string{sniHost},
			},
			Filters: []*listener.Filter{{
				Name: wellknown.TCPProxy,
				ConfigType: &listener.Filter_TypedConfig{
					TypedConfig: pbst,
				},
			}},
		},
	)
	return nil
}

// MakeTCPListener creates a TCP listener for a cluster with SNI indication
func MakeTCPListener(listenerName string, host string, port uint32) *v2.Listener {

	return &v2.Listener{
		Name: listenerName,
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
		ListenerFilters: []*listener.ListenerFilter{
			&listener.ListenerFilter{
				Name: wellknown.TlsInspector,
				ConfigType: &listener.ListenerFilter_TypedConfig{
					TypedConfig: &any.Any{},
				},
			},
		},
		FilterChains: []*listener.FilterChain{},
	}
}
