package data

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/status"
)

type resourceClientCertificates struct {
	caFile, certFile, keyFile string
	server                    *tls.Config
}

// Only the external Resource RPC endpoint is replaced. Production client
// construction, certificates, handshake and RPC cancellation remain real.
func resourceClientTestCertificates(t *testing.T, clientChange func(*x509.Certificate), serverNames []string) resourceClientCertificates {
	t.Helper()
	key := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	now := time.Now()
	caKey := key()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated Resource client test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("test CA did not parse")
	}
	sign := func(template *x509.Certificate) ([]byte, []byte, tls.Certificate) {
		k := key()
		der, err := x509.CreateCertificate(rand.Reader, template, ca, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			t.Fatal(err)
		}
		return certPEM, keyPEM, pair
	}
	client := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"ani-governance"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if clientChange != nil {
		clientChange(client)
	}
	certPEM, keyPEM, _ := sign(client)
	_, _, server := sign(&x509.Certificate{SerialNumber: big.NewInt(3), DNSNames: serverNames, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	dir := t.TempDir()
	f := resourceClientCertificates{caFile: filepath.Join(dir, "ca.pem"), certFile: filepath.Join(dir, "client.pem"), keyFile: filepath.Join(dir, "client-key.pem"), server: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{server}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}}
	for path, data := range map[string][]byte{f.caFile: caPEM, f.certFile: certPEM, f.keyFile: keyPEM} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestResourceClientTLSRejectsAmbiguousLocalIdentity(t *testing.T) {
	cases := []struct {
		name   string
		change func(*x509.Certificate)
	}{
		{"multiple DNS", func(c *x509.Certificate) { c.DNSNames = append(c.DNSNames, "ani-resource-service") }},
		{"duplicate DNS", func(c *x509.Certificate) { c.DNSNames = append(c.DNSNames, "ani-governance") }},
		{"wrong DNS", func(c *x509.Certificate) { c.DNSNames = []string{"ani-resource-service"} }},
		{"wildcard DNS", func(c *x509.Certificate) { c.DNSNames = []string{"*.ani.internal"} }},
		{"CN only", func(c *x509.Certificate) { c.DNSNames = nil; c.Subject.CommonName = "ani-governance" }},
		{"mixed URI", func(c *x509.Certificate) {
			c.URIs = []*url.URL{{Scheme: "spiffe", Host: "ani.internal", Path: "/service/ani-governance"}}
		}},
		{"mixed IP", func(c *x509.Certificate) { c.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")} }},
		{"mixed email", func(c *x509.Certificate) { c.EmailAddresses = []string{"gov@ani.internal"} }},
		{"expired", func(c *x509.Certificate) {
			c.NotBefore = time.Now().Add(-time.Hour)
			c.NotAfter = time.Now().Add(-time.Minute)
		}},
		{"server usage", func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := resourceClientTestCertificates(t, tc.change, []string{"ani-network-service"})
			if _, close, err := NewNetworkClient(NetworkClientConfig{Address: "127.0.0.1:1", CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second}); err == nil {
				close()
				t.Fatal("Network accepted invalid governance identity")
			}
			if _, close, err := NewImageClient(ImageClientConfig{Address: "127.0.0.1:1", CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second}); err == nil {
				close()
				t.Fatal("Image accepted invalid governance identity")
			}
		})
	}
}

type resourceClientTestEndpoint struct {
	networkv1.UnimplementedNetworkServiceServer
	imagev1.UnimplementedTenantImageServiceServer
	called   atomic.Int32
	failure  error
	started  chan struct{}
	finished chan error
	deadline chan time.Time
}

func (s *resourceClientTestEndpoint) answer(ctx context.Context) error {
	s.called.Add(1)
	if s.started != nil {
		if s.deadline != nil {
			deadline, _ := ctx.Deadline()
			s.deadline <- deadline
		}
		select {
		case s.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		s.finished <- ctx.Err()
		return status.FromContextError(ctx.Err()).Err()
	}
	return s.failure
}
func (s *resourceClientTestEndpoint) GetVPC(ctx context.Context, _ *networkv1.GetVPCRequest) (*networkv1.GetVPCResponse, error) {
	return &networkv1.GetVPCResponse{}, s.answer(ctx)
}
func (s *resourceClientTestEndpoint) CreateVPC(ctx context.Context, r *networkv1.CreateVPCRequest) (*networkv1.CreateVPCResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("x-ani-actor")) != 1 || len(md.Get("x-ani-request-id")) != 1 || r.GetAttribution().GetActor() != md.Get("x-ani-actor")[0] || r.GetAttribution().GetDirectCaller() != "ani-governance" || r.GetAttribution().GetCorrelationId() != md.Get("x-ani-request-id")[0] {
		return nil, status.Error(codes.InvalidArgument, "attribution mismatch")
	}
	return &networkv1.CreateVPCResponse{}, s.answer(ctx)
}

func TestResourceClientTLSRejectsUntrustedOrOldTransport(t *testing.T) {
	for _, mode := range []string{"wrong CA", "TLS 1.2"} {
		t.Run(mode, func(t *testing.T) {
			f := resourceClientTestCertificates(t, nil, []string{"ani-network-service"})
			if mode == "wrong CA" {
				untrusted := resourceClientTestCertificates(t, nil, []string{"ani-network-service"})
				f.caFile = untrusted.caFile
			} else {
				f.server.MinVersion = tls.VersionTLS12
				f.server.MaxVersion = tls.VersionTLS12
			}
			endpoint := &resourceClientTestEndpoint{}
			address := resourceClientTestServer(t, f, endpoint)
			network, closeNetwork, err := NewNetworkClient(NetworkClientConfig{Address: address, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeNetwork)
			image, closeImage, err := NewImageClient(ImageClientConfig{Address: address, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeImage)
			_, networkErr := network.GetVPC(context.Background(), "11111111-1111-4111-8111-111111111111", "governance:user:7", "vpc")
			_, imageErr := image.GetImageSpace(context.Background(), "11111111-1111-4111-8111-111111111111", "governance:user:7", &imagev1.GetImageSpaceRequest{})
			if networkErr == nil || imageErr == nil || endpoint.called.Load() != 0 {
				t.Fatalf("untrusted transport reached handlers: Network=%v Image=%v", networkErr, imageErr)
			}
		})
	}
}
func (s *resourceClientTestEndpoint) GetImageSpace(ctx context.Context, _ *imagev1.GetImageSpaceRequest) (*imagev1.GetImageSpaceResponse, error) {
	return &imagev1.GetImageSpaceResponse{}, s.answer(ctx)
}
func (s *resourceClientTestEndpoint) RegisterImage(ctx context.Context, _ *imagev1.RegisterImageRequest) (*imagev1.RegisterImageResponse, error) {
	return &imagev1.RegisterImageResponse{}, s.answer(ctx)
}

func resourceClientTestServer(t *testing.T, f resourceClientCertificates, endpoint *resourceClientTestEndpoint) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(f.server)))
	networkv1.RegisterNetworkServiceServer(server, endpoint)
	imagev1.RegisterTenantImageServiceServer(server, endpoint)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) && !errors.Is(err, net.ErrClosed) {
				t.Errorf("test Resource server shutdown: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("test Resource server did not stop")
		}
	})
	return listener.Addr().String()
}

func TestResourceClientTLSVerifiesLiteralServerIdentity(t *testing.T) {
	for _, names := range [][]string{{"ani-network-service"}, {"ani-resource-service"}, {"ani-network-service", "*.ani.internal"}, {"*.ani.internal"}, nil} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			f := resourceClientTestCertificates(t, nil, names)
			endpoint := &resourceClientTestEndpoint{}
			address := resourceClientTestServer(t, f, endpoint)
			network, closeNetwork, err := NewNetworkClient(NetworkClientConfig{Address: address, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeNetwork)
			image, closeImage, err := NewImageClient(ImageClientConfig{Address: address, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(closeImage)
			_, networkErr := network.GetVPC(context.Background(), "11111111-1111-4111-8111-111111111111", "governance:user:7", "vpc")
			_, imageErr := image.GetImageSpace(context.Background(), "11111111-1111-4111-8111-111111111111", "governance:user:7", &imagev1.GetImageSpaceRequest{})
			valid := len(names) == 1 && names[0] == "ani-network-service"
			if valid {
				if networkErr != nil || imageErr != nil || endpoint.called.Load() != 2 {
					t.Fatalf("registered Resource identity failed: Network=%v Image=%v", networkErr, imageErr)
				}
			} else if networkErr == nil || imageErr == nil || endpoint.called.Load() != 0 {
				t.Fatalf("invalid Resource identity reached handlers: Network=%v Image=%v", networkErr, imageErr)
			}
		})
	}
}

// Advertise a retry policy through a resolver, as DNS TXT/service discovery
// could do. These adapters must ignore it and return the first write failure.
type resourceRetryResolver struct{ scheme, address string }

func (r resourceRetryResolver) Scheme() string { return r.scheme }
func (r resourceRetryResolver) Build(_ resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	config := cc.ParseServiceConfig(`{"methodConfig":[{"name":[{"service":"network.v1.NetworkService","method":"CreateVPC"},{"service":"image.v1.TenantImageService","method":"RegisterImage"}],"retryPolicy":{"MaxAttempts":4,"InitialBackoff":"0.001s","MaxBackoff":"0.001s","BackoffMultiplier":1,"RetryableStatusCodes":["UNAVAILABLE"]}}]}`)
	return r, cc.UpdateState(resolver.State{Addresses: []resolver.Address{{Addr: r.address}}, ServiceConfig: config})
}
func (resourceRetryResolver) ResolveNow(resolver.ResolveNowOptions) {}
func (resourceRetryResolver) Close()                                {}

func TestResourceClientsDoNotReplayFailedWrites(t *testing.T) {
	f := resourceClientTestCertificates(t, nil, []string{"ani-network-service"})
	endpoint := &resourceClientTestEndpoint{failure: status.Error(codes.Unavailable, "write outcome is unknown")}
	address := resourceClientTestServer(t, f, endpoint)
	scheme := "resource" + strings.ReplaceAll(uuid.NewString(), "-", "")
	resolver.Register(resourceRetryResolver{scheme: scheme, address: address})
	target := scheme + ":///resource"
	network, closeNetwork, err := NewNetworkClient(NetworkClientConfig{Address: target, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeNetwork)
	image, closeImage, err := NewImageClient(ImageClientConfig{Address: target, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeImage)
	_, err = network.CreateVPC(context.Background(), "11111111-1111-4111-8111-111111111111", "governance:user:7", "vpc", "10.0.0.0/16", "", "stable-command-id")
	if status.Code(err) != codes.Unavailable || endpoint.called.Load() != 1 {
		t.Fatalf("Network write retried or status changed: calls=%d err=%v", endpoint.called.Load(), err)
	}
	_, err = image.RegisterImage(context.Background(), "11111111-1111-4111-8111-111111111111", "governance:access-key:42", &imagev1.RegisterImageRequest{IdempotencyKey: "stable-command-id"})
	if status.Code(err) != codes.Unavailable || endpoint.called.Load() != 2 {
		t.Fatalf("Image write retried or status changed: calls=%d err=%v", endpoint.called.Load(), err)
	}
}

func TestResourceClientsPropagateCallerCancellation(t *testing.T) {
	for _, kind := range []string{"Network", "Image"} {
		t.Run(kind, func(t *testing.T) {
			f := resourceClientTestCertificates(t, nil, []string{"ani-network-service"})
			endpoint := &resourceClientTestEndpoint{started: make(chan struct{}, 1), finished: make(chan error, 1), deadline: make(chan time.Time, 1)}
			address := resourceClientTestServer(t, f, endpoint)
			var call func(context.Context) error
			if kind == "Network" {
				client, close, err := NewNetworkClient(NetworkClientConfig{Address: address, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: 3 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(close)
				call = func(ctx context.Context) error {
					_, err := client.GetVPC(ctx, "11111111-1111-4111-8111-111111111111", "governance:user:7", "vpc")
					return err
				}
			} else {
				client, close, err := NewImageClient(ImageClientConfig{Address: address, CAFile: f.caFile, CertFile: f.certFile, KeyFile: f.keyFile, Timeout: 3 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(close)
				call = func(ctx context.Context) error {
					_, err := client.GetImageSpace(ctx, "11111111-1111-4111-8111-111111111111", "governance:user:7", &imagev1.GetImageSpaceRequest{})
					return err
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- call(ctx) }()
			select {
			case <-endpoint.started:
				deadline := <-endpoint.deadline
				callerDeadline, _ := ctx.Deadline()
				if deadline.IsZero() || deadline.After(callerDeadline.Add(20*time.Millisecond)) {
					t.Fatal("adapter extended the caller deadline to its own timeout")
				}
				cancel()
			case <-ctx.Done():
				t.Fatal("request did not reach the external endpoint before cancellation")
			}
			select {
			case err := <-done:
				if status.Code(err) != codes.Canceled {
					t.Fatalf("caller cancel changed: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("client did not inherit caller cancellation")
			}
			select {
			case err := <-endpoint.finished:
				if err != context.Canceled {
					t.Fatalf("server cancellation changed: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("external endpoint did not observe caller cancellation")
			}
		})
	}
}

func TestResourceClientConfigRejectsNonpositiveTimeout(t *testing.T) {
	for _, raw := range []string{"0s", "-1s", "invalid"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("ANI_NETWORK_TIMEOUT", raw)
			t.Setenv("ANI_IMAGE_TIMEOUT", raw)
			if _, err := NetworkConfigFromEnv(); err == nil {
				t.Fatal("Network accepted invalid timeout")
			}
			if _, err := ImageConfigFromEnv(); err == nil {
				t.Fatal("Image accepted invalid timeout")
			}
		})
	}
}
