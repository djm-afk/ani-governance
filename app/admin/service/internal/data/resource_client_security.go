package data

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Resource's Network and Image APIs share the registered internal listener.
// This helper only configures these two adapters, not other ANI dependencies.
func resourceClientTLS(caFile, certFile, keyFile string) (*tls.Config, error) {
	if caFile == "" || certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("Resource CA, client certificate and key are required")
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read Resource CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("Resource CA contains no certificates")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load governance client certificate: %w", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("governance client certificate is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse governance client certificate: %w", err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "ani-governance" || len(leaf.URIs) != 0 || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 {
		return nil, fmt.Errorf("governance certificate requires only DNS SAN ani-governance")
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("governance client certificate is not valid now")
	}
	clientUsage := len(leaf.ExtKeyUsage) == 0
	for _, usage := range leaf.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth || usage == x509.ExtKeyUsageAny {
			clientUsage = true
		}
	}
	if !clientUsage {
		return nil, fmt.Errorf("governance certificate does not permit client authentication")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots,
		Certificates: []tls.Certificate{cert}, ServerName: "ani-network-service",
		// Keep default chain, time, usage and hostname verification, then
		// require the registered literal SAN and reject wildcard identities.
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return fmt.Errorf("unverified Resource server certificate")
			}
			matched := false
			for _, dns := range state.PeerCertificates[0].DNSNames {
				if strings.Contains(dns, "*") {
					return fmt.Errorf("wildcard Resource server identity is forbidden")
				}
				matched = matched || dns == "ani-network-service"
			}
			if !matched {
				return fmt.Errorf("Resource server exact DNS SAN mismatch")
			}
			return nil
		},
	}, nil
}

func validResourceIdentity(tenant, actor string) bool {
	id, err := uuid.Parse(tenant)
	if err != nil || id == uuid.Nil || id.String() != tenant {
		return false
	}
	for _, prefix := range []string{"governance:user:", "governance:access-key:"} {
		if strings.HasPrefix(actor, prefix) {
			id, err := strconv.ParseUint(strings.TrimPrefix(actor, prefix), 10, 32)
			return err == nil && id > 0 && actor == prefix+strconv.FormatUint(id, 10)
		}
	}
	return false
}
