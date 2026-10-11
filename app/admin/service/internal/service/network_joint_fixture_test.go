//go:build networkintegration

package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go-wind-admin/app/admin/service/internal/data"
)

type networkResourcePeer struct {
	ProviderURL, CertificateName, Namespace                                               string
	Address, CAFile, CertFile, KeyFile, RuntimeDSN                                        string
	TenantID, ParentVPC, EntrySubnet, BackendSubnet, BackendAddress, SecondBackendAddress string
	LiveClusterUID, VPCCIDR, SubnetCIDR                                                   string
	Client                                                                                *data.NetworkClient
	DB                                                                                    *sql.DB
}

func newNetworkResourcePeer(t *testing.T) *networkResourcePeer {
	t.Helper()
	if info := os.Getenv("NETWORK_JOINT_EXISTING_RESOURCE"); info != "" {
		// The live runner owns this actual Resource process and its isolated DB.
		// The VPC scenario still uses the same HTTP/auth/mTLS and product cleanup.
		selector := flag.Lookup("test.run").Value.String()
		require.True(t, selector == "^TestNetworkJointHTTP/vpc_presets$" || selector == "^TestNetworkJointHTTP/vpc_presets/rejections$", "live Resource is limited to the VPC product scenarios")
		require.True(t, filepath.IsAbs(info))
		raw, err := os.ReadFile(info)
		require.NoError(t, err)
		peer := &networkResourcePeer{}
		require.NoError(t, json.Unmarshal(raw, peer))
		require.NotEmpty(t, peer.LiveClusterUID)
		require.NotEmpty(t, peer.VPCCIDR)
		require.NotEmpty(t, peer.SubnetCIDR)
		host, _, err := net.SplitHostPort(peer.Address)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", host)
		dsn, err := url.Parse(peer.RuntimeDSN)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1", dsn.Hostname())
		require.True(t, strings.HasPrefix(dsn.Path, "/network_presets_live_"), "live runner must use its exclusive database")
		var closeClient func()
		peer.Client, closeClient, err = data.NewNetworkClient(data.NetworkClientConfig{Address: peer.Address, CAFile: peer.CAFile, CertFile: peer.CertFile, KeyFile: peer.KeyFile, Timeout: 5 * time.Second})
		require.NoError(t, err)
		t.Cleanup(closeClient)
		peer.DB, err = sql.Open("pgx", peer.RuntimeDSN)
		require.NoError(t, err)
		t.Cleanup(func() { _ = peer.DB.Close() })
		return peer
	}
	binary := os.Getenv("RESOURCE_NETWORK_TEST_BINARY")
	require.NotEmpty(t, binary, "exact Resource test binary required")
	require.True(t, filepath.IsAbs(binary))
	dir := t.TempDir()
	cmd := exec.Command(binary, "-test.run=^TestNetworkGovernanceFixture$", "-test.timeout=8m", "-test.v")
	cmd.Env = append(os.Environ(), "NETWORK_JOINT_FIXTURE_DIR="+dir)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_, _ = fmt.Fprintln(input, "stop")
		_ = input.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Log(output.String())
			}
			require.NoError(t, err, "Resource fixture product cleanup/isolated DB teardown must pass")
			t.Logf("Resource fixture pid=%d exit=0; product and infrastructure cleanup pass", cmd.Process.Pid)
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("Resource fixture exceeded cleanup deadline")
		}
	})
	var raw []byte
	require.Eventually(t, func() bool { raw, err = os.ReadFile(filepath.Join(dir, "fixture.json")); return err == nil }, 30*time.Second, 20*time.Millisecond, "Resource production fixture startup required")
	peer := &networkResourcePeer{}
	require.NoError(t, json.Unmarshal(raw, peer))
	var closeClient func()
	peer.Client, closeClient, err = data.NewNetworkClient(data.NetworkClientConfig{Address: peer.Address, CAFile: peer.CAFile, CertFile: peer.CertFile, KeyFile: peer.KeyFile, Timeout: 5 * time.Second})
	require.NoError(t, err)
	t.Cleanup(closeClient)
	peer.DB, err = sql.Open("pgx", peer.RuntimeDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.DB.Close() })
	return peer
}
