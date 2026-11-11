package commandcontract

import (
	quota "go-wind-admin/api/gen/go/quota/service/v1"
	"strconv"
	"strings"
)

const (
	ModelDevOwner        = "ani-modeldev-service"
	ModelDevCreateAction = "modeldev.execution.create"
	ModelDevUsage        = "TRAINING"
)

func GPUOwnerUnavailable() error {
	return quota.ErrorQuotaAdapterUnavailable("%s", "GPU business owner is not enabled")
}
func ModelDevLedgerActor(actor string) (string, string, error) {
	const prefix = "governance:user:"
	id, err := strconv.ParseUint(strings.TrimPrefix(actor, prefix), 10, 32)
	if err != nil || id == 0 || actor != prefix+strconv.FormatUint(id, 10) {
		return "", "", Invalid("invalid ModelDev create actor")
	}
	return "user", strconv.FormatUint(id, 10), nil
}

func ValidateModelDevStoredCommand(StoredCommand) (*Facts, error) { return nil, GPUOwnerUnavailable() }
