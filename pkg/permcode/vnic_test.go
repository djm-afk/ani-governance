package permcode

import "testing"

func TestManualVNicAndOwnerNetworkPermissions(t *testing.T) {
	for _, v := range []struct{ method, path, code, module string }{
		{"GET", "/api/v1/networks/instances", "network:instance:list", "NETWORK"},
		{"POST", "/api/v1/networks/vnics", "network:vnic:create", "NETWORK"},
		{"GET", "/api/v1/networks/vnics", "network:vnic:list", "NETWORK"},
		{"GET", "/api/v1/networks/vnics/{vnic_id}", "network:vnic:get", "NETWORK"},
		{"DELETE", "/api/v1/networks/vnics/{vnic_id}", "network:vnic:delete", "NETWORK"},
		{"GET", "/api/v1/networks/vnics/{vnic_id}/binding-targets", "network:vnic:binding-targets", "NETWORK"},
		{"GET", "/api/v1/virtual-machines/{vm_id}/network-interfaces", "vm:network-interfaces:get", "VIRTUAL_MACHINE"},
		{"PUT", "/api/v1/virtual-machines/{vm_id}/network-interfaces/{slot}", "vm:network-interfaces:set", "VIRTUAL_MACHINE"},
		{"GET", "/api/v1/instances/{instance_id}/network-interfaces", "instance:network-interfaces:get", "INSTANCE"},
		{"PUT", "/api/v1/instances/{instance_id}/network-interfaces/{slot}", "instance:network-interfaces:set", "INSTANCE"},
	} {
		got, e := Resolve(v.method, v.path)
		if e != nil || got.Code != v.code || got.BusinessModule != v.module || got.ScopeKind != "tenant" {
			t.Fatal("wrong VNIC grant", v, got, e)
		}
	}
}
