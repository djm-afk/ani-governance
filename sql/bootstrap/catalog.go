// Package bootstrap owns explicit deployment-time data initialization.
// It never runs from service constructors and never creates database tables.
package bootstrap

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"

	"go-wind-admin/pkg/constants"
)

type API struct {
	Path, Method, Operation, Module, ModuleDescription, Description, BusinessModule string
}

// Short labels belong to the management catalog; OpenAPI retains the detailed
// request, authorization and response contracts.
//
//go:embed api_descriptions_zh.json
var apiDescriptionsJSON []byte

// Catalog uses the OpenAPI shipped with this binary, in a deterministic order.
// Initialization and later synchronization share the same display metadata.
func Catalog(document []byte) ([]API, error) {
	doc, err := openapi3.NewLoader().LoadFromData(document)
	if err != nil {
		return nil, fmt.Errorf("load API catalog: %w", err)
	}
	if doc == nil || doc.Paths == nil {
		return nil, fmt.Errorf("API catalog has no paths")
	}
	var descriptions map[string]string
	if err = json.Unmarshal(apiDescriptionsJSON, &descriptions); err != nil {
		return nil, fmt.Errorf("load API catalog descriptions: %w", err)
	}
	var result []API
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			a := API{Path: path, Method: method, Operation: op.OperationID, Description: op.Description}
			if description, ok := descriptions[method+" "+path]; ok {
				a.Description = description
			}
			if len(op.Tags) > 0 {
				a.Module = op.Tags[0]
			}
			if module, ok := constants.ServiceTagToBusinessModule[a.Module]; ok {
				a.BusinessModule = module.String()
			}
			if a.Operation == "" || a.BusinessModule == "" {
				return nil, fmt.Errorf("API %s %s lacks operation or business module mapping", method, path)
			}
			result = append(result, a)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path+" "+result[i].Method < result[j].Path+" "+result[j].Method })
	return result, nil
}

// SyncAPIs preserves IDs, status and permission associations. It never grants or
// deletes anything. Removed routes are reported for a separate reviewed change.
// The caller owns the transaction, allowing init to roll back as a single unit.
func SyncAPIs(ctx context.Context, tx *sql.Tx, catalog []API, dryRun bool) ([]string, error) {
	if !dryRun {
		if _, err := tx.ExecContext(ctx, `LOCK TABLE sys_apis IN EXCLUSIVE MODE`); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(path,''), COALESCE(method,''), COALESCE(operation,''), COALESCE(module,''), COALESCE(module_description,''), COALESCE(description,''), COALESCE(business_module,'') FROM sys_apis ORDER BY id`)
	if err != nil {
		return nil, err
	}
	type existingAPI struct {
		ID uint32
		API
	}
	existing := map[string]existingAPI{}
	for rows.Next() {
		var a existingAPI
		if err = rows.Scan(&a.ID, &a.Path, &a.Method, &a.Operation, &a.Module, &a.ModuleDescription, &a.Description, &a.BusinessModule); err != nil {
			rows.Close()
			return nil, err
		}
		key := a.Method + " " + a.Path
		if _, found := existing[key]; found {
			rows.Close()
			return nil, fmt.Errorf("ambiguous API %s: resolve duplicate rows before sync", key)
		}
		existing[key] = a
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var changes []string
	sequenceChecked := false
	for _, a := range catalog {
		key := a.Method + " " + a.Path
		old, found := existing[key]
		delete(existing, key)
		if found && old.API == a {
			continue
		}
		if dryRun {
			action := "ADD "
			if found {
				action = "UPDATE "
			}
			changes = append(changes, action+key)
			continue
		}
		if found {
			_, err = tx.ExecContext(ctx, `UPDATE sys_apis SET operation=$1,module=$2,module_description=$3,description=$4,business_module=$5,updated_at=NOW() WHERE id=$6`, a.Operation, a.Module, a.ModuleDescription, a.Description, a.BusinessModule, old.ID)
			changes = append(changes, "UPDATE "+key)
		} else {
			// Historical imports assigned explicit IDs without advancing the sequence.
			if !sequenceChecked {
				_, err = tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('sys_apis','id'), GREATEST(COALESCE((SELECT MAX(id) FROM sys_apis),0)+1, nextval(pg_get_serial_sequence('sys_apis','id'))), false)`)
				if err != nil {
					return nil, err
				}
				sequenceChecked = true
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO sys_apis(path,method,operation,module,module_description,description,business_module,scope,status,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'ADMIN','ON',NOW())`, a.Path, a.Method, a.Operation, a.Module, a.ModuleDescription, a.Description, a.BusinessModule)
			changes = append(changes, "ADD "+key)
		}
		if err != nil {
			return nil, fmt.Errorf("sync %s: %w", key, err)
		}
	}
	for key := range existing {
		changes = append(changes, "REVIEW absent from OpenAPI (retained): "+key)
	}
	sort.Strings(changes)
	return changes, nil
}
