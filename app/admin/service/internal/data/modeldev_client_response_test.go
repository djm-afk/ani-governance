package data

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	modeldevv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	trainingv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/training/v1"
	"github.com/zhangzhe-ctrl/ani-modeldev-service/contract/cpup01"
	"github.com/zhangzhe-ctrl/ani-modeldev-service/contract/cpup01/conformance"
	contractpb "github.com/zhangzhe-ctrl/ani-modeldev-service/contract/cpup01/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// These peers are deliberate response/transport substitutes. They test the
// public consumer over actual TLS sockets, not ModelDev's resolver or PG facts.
func TestModelDevResolveRejectsUntrustedResponse(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*modeldevv1.ResolveAdmissionResponse)
	}{
		{"missing snapshot", func(r *modeldevv1.ResolveAdmissionResponse) { r.Snapshot = nil }},
		{"outer unknown", func(r *modeldevv1.ResolveAdmissionResponse) { r.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01}) }},
		{"nested unknown", func(r *modeldevv1.ResolveAdmissionResponse) {
			r.Snapshot.Release.Runtime.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"nested enum", func(r *modeldevv1.ResolveAdmissionResponse) {
			r.Snapshot.OutputContract.RequiredFiles[0].Role = trainingv1.FileRole(999)
		}},
		{"missing digest", func(r *modeldevv1.ResolveAdmissionResponse) { r.ExecutionSpecHash = "" }},
		{"wrong digest", func(r *modeldevv1.ResolveAdmissionResponse) { r.ExecutionSpecHash = strings.Repeat("0", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := modelDevResponseFixture(t)
			bad := proto.Clone(fixture.response).(*modeldevv1.ResolveAdmissionResponse)
			test.change(bad)
			config, calls := startModelDevResponsePeer(t, []string{"ani-modeldev-service"}, fixture, func(context.Context, *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error) {
				return bad, nil
			})
			client := newModelDevBoundaryClient(t, config)
			got, err := fixture.resolve(context.Background(), client)
			assertModelDevSafeFailure(t, got, err, codes.Unavailable, "modeldev admission unavailable", 0)
			if calls.Load() != 1 {
				t.Error("response boundary did not consume exactly one actual TLS peer reply")
			}
		})
	}
}

func TestModelDevResolveRejectsCorrectlyHashedDifferentCandidate(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*cpup01.Snapshot, time.Time)
	}{
		{"selected release", func(s *cpup01.Snapshot, _ time.Time) { s.Release.ReleaseID = uuid.NewString() }},
		{"selected digest", func(s *cpup01.Snapshot, _ time.Time) { s.Release.ReleaseDigest = strings.Repeat("c", 64) }},
		{"binding generation", func(s *cpup01.Snapshot, _ time.Time) { s.Release.AcceptedBindingGeneration++ }},
		{"intent preset", func(s *cpup01.Snapshot, _ time.Time) { s.Release.PresetID = uuid.NewString() }},
		{"intent input", func(s *cpup01.Snapshot, _ time.Time) { s.Input.InputVersionID = uuid.NewString() }},
		{"explicit image", func(s *cpup01.Snapshot, _ time.Time) { s.Program.ImageVersionID = uuid.NewString() }},
		{"explicit parameter", func(s *cpup01.Snapshot, _ time.Time) {
			for i := range s.Program.ResolvedParameters {
				if s.Program.ResolvedParameters[i].Name == "learning_rate" {
					s.Program.ResolvedParameters[i].Value = "0.09"
				}
			}
		}},
		{"deadline does not follow acceptance", func(s *cpup01.Snapshot, at time.Time) { s.DeadlineAt = at }},
		{"deadline loses microseconds", func(s *cpup01.Snapshot, _ time.Time) { s.DeadlineAt = s.DeadlineAt.Add(time.Nanosecond) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := modelDevResponseFixture(t)
			changed, err := contractpb.DecodeSnapshot(fixture.response.Snapshot)
			if err != nil {
				t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: normative response cannot decode")
			}
			test.change(&changed, fixture.acceptedAt)
			digest, err := changed.Digest()
			if err != nil {
				t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: association fixture is not shape-valid")
			}
			wire, err := contractpb.EncodeSnapshot(changed)
			if err != nil || digest == fixture.response.ExecutionSpecHash {
				t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: association mutation has no valid new digest")
			}
			bad := &modeldevv1.ResolveAdmissionResponse{Snapshot: wire, ExecutionSpecHash: digest}
			config, calls := startModelDevResponsePeer(t, []string{"ani-modeldev-service"}, fixture, func(context.Context, *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error) {
				return bad, nil
			})
			got, err := fixture.resolve(context.Background(), newModelDevBoundaryClient(t, config))
			assertModelDevSafeFailure(t, got, err, codes.Unavailable, "modeldev admission unavailable", 0)
			if calls.Load() != 1 {
				t.Error("candidate correspondence was not tested against one actual TLS reply")
			}
		})
	}
}

func TestModelDevResolveSanitizesRemoteStatus(t *testing.T) {
	for _, mode := range []string{"known reason untrusted text", "missing detail", "extra detail", "unknown reason", "wrong code", "wrong correlation", "unknown detail field", "violations"} {
		t.Run(mode, func(t *testing.T) {
			fixture := modelDevResponseFixture(t)
			config, calls := startModelDevResponsePeer(t, []string{"ani-modeldev-service"}, fixture, func(ctx context.Context, _ *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error) {
				md, _ := metadata.FromIncomingContext(ctx)
				ids := md.Get("x-ani-request-id")
				if len(ids) != 1 {
					return nil, status.Error(codes.Internal, "remote-private-status-sentinel")
				}
				detail := &modeldevv1.ErrorDetail{Reason: modeldevv1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND, CorrelationId: ids[0], SafeMessage: "remote-private-status-sentinel"}
				code := codes.NotFound
				switch mode {
				case "missing detail":
					return nil, status.Error(code, "remote-private-status-sentinel")
				case "unknown reason":
					detail.Reason = modeldevv1.ErrorReason(999)
				case "wrong code":
					code = codes.InvalidArgument
				case "wrong correlation":
					detail.CorrelationId = uuid.NewString()
				case "unknown detail field":
					detail.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
				case "violations":
					detail.Violations = []*modeldevv1.FieldViolation{{Field: "private", Reason: "remote-private-status-sentinel"}}
				}
				failure, err := status.New(code, "remote-private-status-sentinel").WithDetails(detail)
				if err != nil {
					return nil, status.Error(codes.Internal, "substitute detail encoding failed")
				}
				if mode == "extra detail" {
					failure, err = failure.WithDetails(detail)
				}
				if err != nil {
					return nil, status.Error(codes.Internal, "substitute detail encoding failed")
				}
				return nil, failure.Err()
			})
			got, err := fixture.resolve(context.Background(), newModelDevBoundaryClient(t, config))
			if mode == "known reason untrusted text" {
				assertModelDevSafeFailure(t, got, err, codes.NotFound, "input version unavailable", modeldevv1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND)
			} else {
				assertModelDevSafeFailure(t, got, err, codes.Unavailable, "modeldev admission unavailable", 0)
			}
			if err != nil && strings.Contains(err.Error(), "remote-private-status-sentinel") {
				t.Error("client returned remote error text")
			}
			if calls.Load() != 1 {
				t.Error("safe error mapping did not consume the actual peer status")
			}
		})
	}
}

func TestModelDevResolveCancellationReachesTLSPeer(t *testing.T) {
	for _, mode := range []string{"caller cancellation", "caller deadline", "client timeout"} {
		t.Run(mode, func(t *testing.T) {
			fixture := modelDevResponseFixture(t)
			entered, exited := make(chan struct{}, 1), make(chan error, 1)
			config, _ := startModelDevResponsePeer(t, []string{"ani-modeldev-service"}, fixture, func(ctx context.Context, _ *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error) {
				entered <- struct{}{}
				<-ctx.Done()
				exited <- ctx.Err()
				return nil, status.FromContextError(ctx.Err()).Err()
			})
			config.Timeout = 2 * time.Second
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "caller deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			}
			if mode == "client timeout" {
				config.Timeout = time.Second
			}
			defer cancel()
			client := newModelDevBoundaryClient(t, config)
			type outcome struct {
				result ModelDevResolution
				err    error
			}
			done := make(chan outcome, 1)
			go func() { result, err := fixture.resolve(ctx, client); done <- outcome{result, err} }()
			select {
			case <-entered:
			case got := <-done:
				t.Fatalf("MODELDEV_CANCEL_BEHAVIOR: client returned %s before reaching the independently proven TLS peer", status.Code(got.err))
			case <-time.After(3 * time.Second):
				t.Fatal("MODELDEV_CANCEL_BEHAVIOR: public client never reached the independently proven TLS peer")
			}
			if mode == "caller cancellation" {
				cancel()
			}
			code, message := codes.DeadlineExceeded, "context deadline exceeded"
			if mode == "caller cancellation" {
				code, message = codes.Canceled, "context canceled"
			}
			select {
			case got := <-done:
				assertModelDevSafeFailure(t, got.result, got.err, code, message, 0)
			case <-time.After(3 * time.Second):
				t.Fatal("canceled client did not finish within the bound")
			}
			select {
			case err := <-exited:
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					t.Error("peer ended without a propagated context cause")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("client cancellation did not reach the actual server context")
			}
		})
	}
}

func TestModelDevClientRequiresFixedTLSIdentityAndMaterials(t *testing.T) {
	fixture := modelDevResponseFixture(t)
	for _, test := range []struct {
		name  string
		names []string
		valid bool
	}{
		{"literal plus additional DNS", []string{"ani-modeldev-service", "extra.test"}, true},
		{"literal plus wildcard DNS", []string{"ani-modeldev-service", "*.ani.internal"}, false},
		{"different server DNS", []string{"other-modeldev.test"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, calls := startModelDevResponsePeer(t, test.names, fixture, func(context.Context, *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error) {
				return proto.Clone(fixture.response).(*modeldevv1.ResolveAdmissionResponse), nil
			})
			got, err := fixture.resolve(context.Background(), newModelDevBoundaryClient(t, config))
			if test.valid {
				if err != nil || got.ExecutionSpecHash != fixture.response.ExecutionSpecHash || calls.Load() != 1 {
					t.Errorf("valid literal TLS identity was rejected: code=%s", status.Code(err))
				}
			} else {
				assertModelDevSafeFailure(t, got, err, codes.Unavailable, "modeldev admission unavailable", 0)
				if calls.Load() != 0 {
					t.Error("wrong server identity reached the application")
				}
			}
		})
	}
	config, _ := modelDevBoundaryTLS(t, []string{"ani-modeldev-service"}, []string{"ani-governance"})
	config.Address = "127.0.0.1:1"
	for _, test := range []struct {
		name    string
		change  func(*ModelDevClientConfig)
		message string
	}{
		{"missing address", func(c *ModelDevClientConfig) { c.Address = "" }, "invalid modeldev client configuration"},
		{"nonpositive timeout", func(c *ModelDevClientConfig) { c.Timeout = 0 }, "invalid modeldev client configuration"},
		{"missing CA", func(c *ModelDevClientConfig) { c.CAFile = filepath.Join(t.TempDir(), "private-ca-sentinel") }, "modeldev TLS configuration unavailable"},
		{"missing client key", func(c *ModelDevClientConfig) { c.KeyFile = filepath.Join(t.TempDir(), "private-key-sentinel") }, "modeldev TLS configuration unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := config
			test.change(&bad)
			client, closeClient, err := NewModelDevClient(bad)
			if closeClient != nil {
				closeClient()
			}
			if err == nil || err.Error() != test.message || client != nil || closeClient != nil {
				t.Error("invalid configuration was accepted, retained cleanup, or exposed material details")
			}
		})
	}
	bad, _ := modelDevBoundaryTLS(t, []string{"ani-modeldev-service"}, []string{"ani-governance", "another-workload"})
	bad.Address = "127.0.0.1:1"
	client, closeClient, err := NewModelDevClient(bad)
	if closeClient != nil {
		closeClient()
	}
	if err == nil || err.Error() != "modeldev TLS configuration unavailable" || client != nil || closeClient != nil {
		t.Error("ambiguous Governance identity was accepted, retained cleanup, or leaked certificate details")
	}
}

type modelDevResponseCase struct {
	scope      ModelDevResolveScope
	intent     cpup01.Intent
	selection  ModelDevReleaseSelection
	acceptedAt time.Time
	response   *modeldevv1.ResolveAdmissionResponse
}

func modelDevResponseFixture(t *testing.T) modelDevResponseCase {
	t.Helper()
	snapshot := conformance.SnapshotV1()
	image := snapshot.Program.ImageVersionID
	parameters := append([]cpup01.Parameter(nil), snapshot.Program.ResolvedParameters...)
	fixture := modelDevResponseCase{
		scope:      ModelDevResolveScope{ResourceTenantID: uuid.NewString(), Actor: "governance:user:42"},
		intent:     cpup01.Intent{Name: "response-boundary", Kind: snapshot.Kind, PresetID: snapshot.Release.PresetID, DatasetVersionID: snapshot.Input.InputVersionID, ImageVersionID: &image, GeneralParameters: &parameters},
		selection:  ModelDevReleaseSelection{ReleaseID: snapshot.Release.ReleaseID, ReleaseDigest: snapshot.Release.ReleaseDigest, BindingGeneration: snapshot.Release.AcceptedBindingGeneration},
		acceptedAt: snapshot.DeadlineAt.Add(-time.Hour),
	}
	wire, err := contractpb.EncodeSnapshot(snapshot)
	if err != nil {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: normative snapshot invalid")
	}
	fixture.response = &modeldevv1.ResolveAdmissionResponse{Snapshot: wire, ExecutionSpecHash: conformance.SnapshotSHA256V1}
	if _, _, err := cpup01.CanonicalIntent(fixture.intent); err != nil {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: normative intent invalid")
	}
	return fixture
}

func (f modelDevResponseCase) resolve(ctx context.Context, client *ModelDevClient) (ModelDevResolution, error) {
	return client.Resolve(ctx, f.scope, f.intent, f.selection, f.acceptedAt)
}

func newModelDevBoundaryClient(t *testing.T, config ModelDevClientConfig) *ModelDevClient {
	t.Helper()
	client, closeClient, err := NewModelDevClient(config)
	if err != nil || client == nil || closeClient == nil {
		t.Fatal("valid explicit client materials did not construct a client")
	}
	t.Cleanup(closeClient)
	return client
}

func assertModelDevSafeFailure(t *testing.T, got ModelDevResolution, err error, code codes.Code, message string, reason modeldevv1.ErrorReason) {
	t.Helper()
	if !reflect.DeepEqual(got, ModelDevResolution{}) || status.Code(err) != code || status.Convert(err).Message() != message {
		t.Errorf("client failure has candidate=%t code=%s; want zero candidate and finite %s message", !reflect.DeepEqual(got, ModelDevResolution{}), status.Code(err), code)
		return
	}
	details := status.Convert(err).Details()
	if reason == 0 {
		if len(details) != 0 {
			t.Error("generic/context failure retained remote details")
		}
		return
	}
	if len(details) != 1 {
		t.Error("known business failure lacks exactly one safe detail")
		return
	}
	detail, ok := details[0].(*modeldevv1.ErrorDetail)
	if !ok {
		t.Error("known failure changed its detail type")
		return
	}
	id, parseErr := uuid.Parse(detail.CorrelationId)
	if detail.Reason != reason || detail.SafeMessage != message || len(detail.Violations) != 0 || len(detail.ProtoReflect().GetUnknown()) != 0 || parseErr != nil || id == uuid.Nil || id.String() != detail.CorrelationId {
		t.Error("known failure did not reconstruct its finite detail and fresh correlation")
	}
}

type modelDevResponsePeer struct {
	modeldevv1.UnimplementedModelDevAdmissionServiceServer
	baseline *modeldevv1.ResolveAdmissionResponse
	handle   func(context.Context, *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error)
	calls    atomic.Int32
}

func (peer *modelDevResponsePeer) ResolveAdmission(ctx context.Context, request *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error) {
	if request.Intent.GetName() == "transport-preflight" {
		return proto.Clone(peer.baseline).(*modeldevv1.ResolveAdmissionResponse), nil
	}
	peer.calls.Add(1)
	return peer.handle(ctx, request)
}

func startModelDevResponsePeer(t *testing.T, names []string, fixture modelDevResponseCase, handle func(context.Context, *modeldevv1.ResolveAdmissionRequest) (*modeldevv1.ResolveAdmissionResponse, error)) (ModelDevClientConfig, *atomic.Int32) {
	t.Helper()
	config, security := modelDevBoundaryTLS(t, names, []string{"ani-governance"})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: TLS socket unavailable")
	}
	config.Address = listener.Addr().String()
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(security)))
	peer := &modelDevResponsePeer{baseline: fixture.response, handle: handle}
	modeldevv1.RegisterModelDevAdmissionServiceServer(server, peer)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Error("TLS substitute did not stop cleanly")
			}
		case <-time.After(2 * time.Second):
			t.Error("TLS substitute stop exceeded its bound")
		}
	})
	// A generated client proves certificate/socket setup before the product
	// client is exercised. This is explicitly not a real resolver preflight.
	connection := modelDevDirectTLSConnection(t, config, names[0])
	intent, err := contractpb.EncodeIntent(fixture.intent)
	if err != nil {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: cannot encode normative request")
	}
	intent.Name = "transport-preflight"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := modeldevv1.NewModelDevAdmissionServiceClient(connection).ResolveAdmission(ctx, &modeldevv1.ResolveAdmissionRequest{Intent: intent, Release: &modeldevv1.AdmissionReleaseSelection{ReleaseId: fixture.selection.ReleaseID, ReleaseDigest: fixture.selection.ReleaseDigest, BindingGeneration: fixture.selection.BindingGeneration}, AcceptedAt: timestamppb.New(fixture.acceptedAt)}, grpc.WaitForReady(true))
	if err != nil || !proto.Equal(response, fixture.response) {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: actual TLS substitute/control reply failed; behavior NOT_RUN")
	}
	return config, &peer.calls
}

func modelDevDirectTLSConnection(t *testing.T, config ModelDevClientConfig, serverName string) *grpc.ClientConn {
	t.Helper()
	ca, err := os.ReadFile(config.CAFile)
	if err != nil {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: CA material unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: CA material invalid")
	}
	certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: client material invalid")
	}
	connection, err := grpc.NewClient(config.Address, grpc.WithDisableServiceConfig(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}, ServerName: serverName})))
	if err != nil {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: channel configuration failed")
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

// Only this test's private CA/keys are written. No real provider material or
// database credential is copied, printed, or exported by the response tests.
func modelDevBoundaryTLS(t *testing.T, serverNames, clientNames []string) (ModelDevClientConfig, *tls.Config) {
	t.Helper()
	directory := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ModelDev response substitute CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, public, private)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caFile := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	issue := func(label string, serial int64, names []string, usage x509.ExtKeyUsage) (string, string, tls.Certificate) {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: names, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		encoded, err := x509.CreateCertificate(rand.Reader, leaf, ca, pub, private)
		if err != nil {
			t.Fatal(err)
		}
		keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		certFile, keyFile := filepath.Join(directory, label+".pem"), filepath.Join(directory, label+".key")
		if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded}), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), 0600); err != nil {
			t.Fatal(err)
		}
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			t.Fatal(err)
		}
		return certFile, keyFile, pair
	}
	_, _, server := issue("server", 2, serverNames, x509.ExtKeyUsageServerAuth)
	certFile, keyFile, _ := issue("governance", 3, clientNames, x509.ExtKeyUsageClientAuth)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("MODELDEV_RESPONSE_PREFLIGHT: generated CA invalid")
	}
	return ModelDevClientConfig{CAFile: caFile, CertFile: certFile, KeyFile: keyFile, Timeout: time.Second}, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{server}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}
}
