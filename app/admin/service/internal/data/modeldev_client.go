package data

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	modeldevv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	"github.com/zhangzhe-ctrl/ani-modeldev-service/contract/cpup01"
	contractpb "github.com/zhangzhe-ctrl/ani-modeldev-service/contract/cpup01/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Registered service identities for the Governance -> ModelDev internal RPC.
// See docs/interface-integration-register.md (S2S-MODELDEV-CPUP01-01): the
// client presents one exact DNS SAN and verifies the literal ModelDev server
// DNS SAN. Neither is supplied by a public request.
const (
	modelDevClientDNSName = "ani-governance"
	modelDevServerDNSName = "ani-modeldev-service"
)

// ModelDevClientConfig is explicit internal connection material. The server
// identity is fixed to ani-modeldev-service, never supplied by a public request.
type ModelDevClientConfig struct {
	Address, CAFile, CertFile, KeyFile string
	Timeout                            time.Duration
}

// ModelDevResolveScope is supplied after current Governance authorization and
// its persisted tenant mapping. It is not a public request DTO.
type ModelDevResolveScope struct {
	ResourceTenantID string
	Actor            string
}

// ModelDevReleaseSelection is one current binding observation. ModelDev must
// not choose another Release or allocate Governance's binding generation.
type ModelDevReleaseSelection struct {
	ReleaseID         string
	ReleaseDigest     string
	BindingGeneration uint64
}

// ModelDevResolution is an unpersisted candidate, not a command receipt.
type ModelDevResolution struct {
	Snapshot          cpup01.Snapshot
	ExecutionSpecHash string
}

type ModelDevClient struct {
	connection *grpc.ClientConn
	client     modeldevv1.ModelDevAdmissionServiceClient
	timeout    time.Duration
}

func NewModelDevClient(config ModelDevClientConfig) (*ModelDevClient, func(), error) {
	if config.Address == "" || strings.TrimSpace(config.Address) != config.Address || config.Timeout <= 0 {
		return nil, nil, errors.New("invalid modeldev client configuration")
	}
	security, err := modelDevClientTLS(config)
	if err != nil {
		return nil, nil, err
	}
	connection, err := grpc.NewClient(config.Address, grpc.WithDisableServiceConfig(), grpc.WithDisableRetry(), grpc.WithTransportCredentials(credentials.NewTLS(security)))
	if err != nil {
		return nil, nil, errors.New("invalid modeldev client configuration")
	}
	client := &ModelDevClient{connection: connection, client: modeldevv1.NewModelDevAdmissionServiceClient(connection), timeout: config.Timeout}
	return client, func() { _ = connection.Close() }, nil
}

func modelDevClientTLS(config ModelDevClientConfig) (*tls.Config, error) {
	unavailable := errors.New("modeldev TLS configuration unavailable")
	pem, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, unavailable
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, unavailable
	}
	certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil || len(certificate.Certificate) == 0 {
		return nil, unavailable
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != modelDevClientDNSName || len(leaf.IPAddresses) != 0 || len(leaf.URIs) != 0 || len(leaf.EmailAddresses) != 0 {
		return nil, unavailable
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, unavailable
	}
	clientUsage := len(leaf.ExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth || usage == x509.ExtKeyUsageAny {
			clientUsage = true
		}
	}
	if !clientUsage {
		return nil, unavailable
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}, ServerName: modelDevServerDNSName,
		// Normal TLS verification checks the chain, usage, time and ServerName.
		// Also require the literal service SAN rather than a wildcard identity.
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return unavailable
			}
			matched := false
			for _, name := range state.PeerCertificates[0].DNSNames {
				if strings.Contains(name, "*") {
					return unavailable
				}
				matched = matched || name == modelDevServerDNSName
			}
			if !matched {
				return unavailable
			}
			return nil
		},
	}, nil
}

func (c *ModelDevClient) Resolve(ctx context.Context, scope ModelDevResolveScope, intent cpup01.Intent, selection ModelDevReleaseSelection, acceptedAt time.Time) (ModelDevResolution, error) {
	if err := ctx.Err(); err != nil {
		return ModelDevResolution{}, status.FromContextError(err).Err()
	}
	request, normalized, err := modelDevResolutionRequest(scope, intent, selection, acceptedAt)
	if err != nil {
		return ModelDevResolution{}, err
	}
	if c == nil || c.connection == nil || c.client == nil || c.timeout <= 0 {
		return ModelDevResolution{}, modelDevAdmissionUnavailable()
	}
	callContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	requestID := uuid.NewString()
	callContext = metadata.NewOutgoingContext(callContext, metadata.Pairs(
		"x-ani-tenant-id", scope.ResourceTenantID, "x-ani-actor", scope.Actor, "x-ani-request-id", requestID,
	))
	reply, err := c.client.ResolveAdmission(callContext, request, grpc.WaitForReady(true))
	if contextErr := ctx.Err(); contextErr != nil {
		return ModelDevResolution{}, status.FromContextError(contextErr).Err()
	}
	if callContext.Err() != nil {
		if c.connection.GetState() != connectivity.Ready {
			return ModelDevResolution{}, modelDevAdmissionUnavailable()
		}
		return ModelDevResolution{}, status.FromContextError(callContext.Err()).Err()
	}
	if err != nil {
		return ModelDevResolution{}, modelDevResolutionError(err, requestID)
	}
	resolved, err := validateModelDevResolution(reply, normalized, request.Release, request.AcceptedAt.AsTime())
	if contextErr := ctx.Err(); contextErr != nil {
		return ModelDevResolution{}, status.FromContextError(contextErr).Err()
	}
	if err != nil {
		return ModelDevResolution{}, err
	}
	return resolved, nil
}

func modelDevResolutionRequest(scope ModelDevResolveScope, intent cpup01.Intent, selection ModelDevReleaseSelection, acceptedAt time.Time) (*modeldevv1.ResolveAdmissionRequest, cpup01.Intent, error) {
	invalid := status.Error(codes.InvalidArgument, "invalid modeldev resolution request")
	tenant, tenantOK := canonicalModelDevBindingUUID(scope.ResourceTenantID)
	release, releaseOK := canonicalModelDevBindingUUID(selection.ReleaseID)
	digest, digestErr := hex.DecodeString(selection.ReleaseDigest)
	acceptedAt = acceptedAt.UTC()
	if !tenantOK || tenant != scope.ResourceTenantID || !validModelDevClientActor(scope.Actor) || !releaseOK ||
		digestErr != nil || len(digest) != sha256.Size || strings.ToLower(selection.ReleaseDigest) != selection.ReleaseDigest || selection.BindingGeneration == 0 ||
		acceptedAt.IsZero() || acceptedAt.Year() < 1 || acceptedAt.Year() > 9999 || acceptedAt.Nanosecond()%1000 != 0 {
		return nil, cpup01.Intent{}, invalid
	}
	canonical, _, err := cpup01.CanonicalIntent(intent)
	if err != nil {
		return nil, cpup01.Intent{}, invalid
	}
	// Shared canonical bytes preserve optional presence and normalize UUIDs and
	// decimal values. Decode a fresh value so the caller retains its own slices.
	var normalized struct {
		Schema string `json:"schema"`
		cpup01.Intent
	}
	if json.Unmarshal(canonical, &normalized) != nil {
		return nil, cpup01.Intent{}, invalid
	}
	wireIntent, err := contractpb.EncodeIntent(normalized.Intent)
	if err != nil {
		return nil, cpup01.Intent{}, invalid
	}
	return &modeldevv1.ResolveAdmissionRequest{
		Intent:     wireIntent,
		Release:    &modeldevv1.AdmissionReleaseSelection{ReleaseId: release, ReleaseDigest: selection.ReleaseDigest, BindingGeneration: selection.BindingGeneration},
		AcceptedAt: timestamppb.New(acceptedAt),
	}, normalized.Intent, nil
}

func validModelDevClientActor(actor string) bool {
	for _, prefix := range []string{"governance:user:", "governance:access-key:"} {
		if strings.HasPrefix(actor, prefix) {
			id, err := strconv.ParseUint(strings.TrimPrefix(actor, prefix), 10, 32)
			return err == nil && id != 0 && actor == prefix+strconv.FormatUint(id, 10)
		}
	}
	return false
}

func validateModelDevResolution(reply *modeldevv1.ResolveAdmissionResponse, intent cpup01.Intent, release *modeldevv1.AdmissionReleaseSelection, acceptedAt time.Time) (ModelDevResolution, error) {
	if reply == nil || len(reply.ProtoReflect().GetUnknown()) != 0 {
		return ModelDevResolution{}, modelDevAdmissionUnavailable()
	}
	snapshot, err := contractpb.DecodeSnapshot(reply.Snapshot)
	if err != nil {
		return ModelDevResolution{}, modelDevAdmissionUnavailable()
	}
	canonical, err := snapshot.Canonical()
	if err != nil {
		return ModelDevResolution{}, modelDevAdmissionUnavailable()
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != reply.ExecutionSpecHash {
		return ModelDevResolution{}, modelDevAdmissionUnavailable()
	}
	var normalized cpup01.Snapshot
	if json.Unmarshal(canonical, &normalized) != nil ||
		normalized.Release.ReleaseID != release.ReleaseId || normalized.Release.ReleaseDigest != release.ReleaseDigest || normalized.Release.AcceptedBindingGeneration != release.BindingGeneration ||
		normalized.Kind != intent.Kind || normalized.Release.PresetID != intent.PresetID || normalized.Input.InputVersionID != intent.DatasetVersionID ||
		normalized.DeadlineAt.Nanosecond()%1000 != 0 || !normalized.DeadlineAt.After(acceptedAt) {
		return ModelDevResolution{}, modelDevAdmissionUnavailable()
	}
	if intent.ImageVersionID != nil && *intent.ImageVersionID != normalized.Program.ImageVersionID {
		return ModelDevResolution{}, modelDevAdmissionUnavailable()
	}
	if intent.GeneralParameters != nil {
		for _, parameter := range *intent.GeneralParameters {
			matched := false
			for _, resolved := range normalized.Program.ResolvedParameters {
				if parameter == resolved {
					matched = true
					break
				}
			}
			if !matched {
				return ModelDevResolution{}, modelDevAdmissionUnavailable()
			}
		}
	}
	return ModelDevResolution{Snapshot: normalized, ExecutionSpecHash: reply.ExecutionSpecHash}, nil
}

func modelDevAdmissionUnavailable() error {
	return status.Error(codes.Unavailable, "modeldev admission unavailable")
}

func modelDevResolutionError(err error, requestID string) error {
	failure, ok := status.FromError(err)
	if !ok || len(failure.Proto().ProtoReflect().GetUnknown()) != 0 {
		return modelDevAdmissionUnavailable()
	}
	if len(failure.Proto().Details) == 0 {
		switch failure.Code() {
		case codes.Canceled:
			return status.FromContextError(context.Canceled).Err()
		case codes.DeadlineExceeded:
			return status.FromContextError(context.DeadlineExceeded).Err()
		default:
			return modelDevAdmissionUnavailable()
		}
	}
	wire := failure.Proto()
	if len(wire.Details) != 1 || wire.Details[0] == nil || len(wire.Details[0].ProtoReflect().GetUnknown()) != 0 {
		return modelDevAdmissionUnavailable()
	}
	details := failure.Details()
	if len(details) != 1 {
		return modelDevAdmissionUnavailable()
	}
	detail, ok := details[0].(*modeldevv1.ErrorDetail)
	if !ok || detail == nil || len(detail.ProtoReflect().GetUnknown()) != 0 || len(detail.Violations) != 0 || detail.CorrelationId != requestID {
		return modelDevAdmissionUnavailable()
	}
	var message string
	switch {
	case failure.Code() == codes.InvalidArgument && detail.Reason == modeldevv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT:
		message = "invalid modeldev resolution request"
	case failure.Code() == codes.FailedPrecondition && detail.Reason == modeldevv1.ErrorReason_ERROR_REASON_NO_COMPATIBLE_RELEASE:
		message = "selected Release is unavailable or incompatible"
	case failure.Code() == codes.NotFound && detail.Reason == modeldevv1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND:
		message = "input version unavailable"
	case failure.Code() == codes.FailedPrecondition && detail.Reason == modeldevv1.ErrorReason_ERROR_REASON_INPUT_NOT_READY:
		message = "input version is not ready"
	case failure.Code() == codes.Unavailable && detail.Reason == modeldevv1.ErrorReason_ERROR_REASON_ENVIRONMENT_NOT_READY:
		message = "managed admission environment is not ready"
	case failure.Code() == codes.Unavailable && detail.Reason == modeldevv1.ErrorReason_ERROR_REASON_UPSTREAM_UNAVAILABLE:
		message = "admission resolution unavailable"
	default:
		return modelDevAdmissionUnavailable()
	}
	// Remote message text and violations never become the public error surface.
	rebuilt, detailErr := status.New(failure.Code(), message).WithDetails(&modeldevv1.ErrorDetail{
		Reason: detail.Reason, SafeMessage: message, CorrelationId: requestID,
	})
	if detailErr != nil {
		return modelDevAdmissionUnavailable()
	}
	return rebuilt.Err()
}
