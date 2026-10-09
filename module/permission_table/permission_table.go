// Package permission_table prints the grants the module's permission check
// reads, as the permission table SpecArch's extract permissions reads
// (format version 1, the object its dump-permissions.sh wraps).
//
// The check is self.DxmSelf.RegenerateSessionObject: for each role a user
// holds it reads the role_privilege rows that are not deleted, by role_id,
// and takes their privilege_nameid. It does not look at whether the role
// itself is deleted, so the role names here are read with deleted roles
// included. The privilege EVERYTHING is printed as it is stored and not
// expanded into every privilege, since what it grants changes with the
// privilege table.
//
// A service prints the table once its databases are open, for example
// from a command-line flag that prints and exits:
//
//	err := permission_table.Print(ctx, &log.Log, os.Stdout)
//
// and passes the gates of its own middleware, if any, after the writer.
//
// dxlib's log writes to standard output, and dump-permissions.sh wants the
// table's first line { alone, so a command that prints to standard output
// must keep every log line off it; writing the table to a file and printing
// that file afterwards is the safe way.
package permission_table

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
	"github.com/donnyhardyanto/dxlib_module/module/user_management"
)

// Grant is one role and one privilege it grants, by their name ids.
type Grant struct {
	Role       string `json:"role"`
	Permission string `json:"permission"`
}

// Gate is a permission check that runs only when a setting is present, and
// so lets every request through while the setting is empty: the check's
// name and the setting's.
type Gate struct {
	Check   string `json:"check"`
	Setting string `json:"setting"`
}

// ModuleGates returns the gates of the module's own middleware: none.
//
// self.DxmSelf.MiddlewareUserLoggedAndPrivilegeCheck refuses a request
// without a session whatever the settings say, and CheckMaintenanceMode
// only adds a refusal while the global store's system mode is maintenance;
// with the mode empty it still runs CheckUserPrivilegeForEndPoint. An
// endpoint with no privileges, or with no rate-limit group, is passed
// through by the endpoint's own declaration, not by a setting, and the
// endpoint's OpenAPI document already shows it.
//
// Keep this list in step with those checks: a check added to the module
// that skips itself while a setting is empty is declared here.
func ModuleGates() []Gate {
	return []Gate{}
}

// GrantsFromRows makes the grants from the role rows (id and nameid) and
// the role_privilege rows (role_id and privilege_nameid). A role_privilege
// row whose role is not among the roles is an error, since its grant could
// not be named.
func GrantsFromRows(roles []utils.JSON, rolePrivileges []utils.JSON) ([]Grant, error) {
	roleNameIds := map[int64]string{}
	for _, role := range roles {
		id, err := utils.GetInt64FromKV(role, "id")
		if err != nil {
			return nil, fmt.Errorf("role row: %w", err)
		}
		nameId, err := utils.GetStringFromKV(role, "nameid")
		if err != nil {
			return nil, fmt.Errorf("role %d: %w", id, err)
		}
		roleNameIds[id] = nameId
	}
	grants := make([]Grant, 0, len(rolePrivileges))
	for _, rolePrivilege := range rolePrivileges {
		roleId, err := utils.GetInt64FromKV(rolePrivilege, "role_id")
		if err != nil {
			return nil, fmt.Errorf("role_privilege row: %w", err)
		}
		privilegeNameId, err := utils.GetStringFromKV(rolePrivilege, "privilege_nameid")
		if err != nil {
			return nil, fmt.Errorf("role_privilege row of role %d: %w", roleId, err)
		}
		roleNameId, ok := roleNameIds[roleId]
		if !ok {
			return nil, fmt.Errorf("role_privilege row grants %s to role %d, which is not in the role table", privilegeNameId, roleId)
		}
		grants = append(grants, Grant{Role: roleNameId, Permission: privilegeNameId})
	}
	return grants, nil
}

// ReadGrants reads the grants from the module's role and role_privilege
// tables, as the permission check reads them.
func ReadGrants(ctx context.Context, l *log.DXLog) ([]Grant, error) {
	um := &user_management.ModuleUserManagement
	_, roles, err := um.Role.DXRawTable.Select(ctx, l, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	_, rolePrivileges, err := um.RolePrivilege.Select(ctx, l, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	return GrantsFromRows(roles, rolePrivileges)
}

// Print reads the grants and writes the permission table to w, with the
// module's gates and the service's own.
func Print(ctx context.Context, l *log.DXLog, w io.Writer, serviceGates ...Gate) error {
	grants, err := ReadGrants(ctx, l)
	if err != nil {
		return err
	}
	return Write(w, grants, append(ModuleGates(), serviceGates...))
}

// Write writes the permission table: one JSON object, its first line {
// alone, with the grants sorted by role and then permission and the gates
// by check and then setting, one to a line, each listed once. A grant or a
// gate with an empty name is refused.
func Write(w io.Writer, grants []Grant, gates []Gate) error {
	sortedGrants := make([]Grant, 0, len(grants))
	seenGrants := map[Grant]bool{}
	for _, g := range grants {
		if g.Role == "" || g.Permission == "" {
			return fmt.Errorf("a grant names no role or no permission: %+v", g)
		}
		if !seenGrants[g] {
			seenGrants[g] = true
			sortedGrants = append(sortedGrants, g)
		}
	}
	sort.Slice(sortedGrants, func(i, j int) bool {
		if sortedGrants[i].Role != sortedGrants[j].Role {
			return sortedGrants[i].Role < sortedGrants[j].Role
		}
		return sortedGrants[i].Permission < sortedGrants[j].Permission
	})

	sortedGates := make([]Gate, 0, len(gates))
	seenGates := map[Gate]bool{}
	for _, g := range gates {
		if g.Check == "" || g.Setting == "" {
			return fmt.Errorf("a gate names no check or no setting: %+v", g)
		}
		if !seenGates[g] {
			seenGates[g] = true
			sortedGates = append(sortedGates, g)
		}
	}
	sort.Slice(sortedGates, func(i, j int) bool {
		if sortedGates[i].Check != sortedGates[j].Check {
			return sortedGates[i].Check < sortedGates[j].Check
		}
		return sortedGates[i].Setting < sortedGates[j].Setting
	})

	var b bytes.Buffer
	b.WriteString("{\n    \"grants\": ")
	if err := writeList(&b, sortedGrants, func(g Grant) [2][2]string {
		return [2][2]string{{"role", g.Role}, {"permission", g.Permission}}
	}); err != nil {
		return err
	}
	b.WriteString(",\n    \"gates\": ")
	if err := writeList(&b, sortedGates, func(g Gate) [2][2]string {
		return [2][2]string{{"check", g.Check}, {"setting", g.Setting}}
	}); err != nil {
		return err
	}
	b.WriteString("\n}\n")
	_, err := w.Write(b.Bytes())
	return err
}

// writeList writes items as a JSON list, one item to a line, or [] when
// there are none. Each item is two string members, written as
// {"role": "a", "permission": "b"}.
func writeList[T any](b *bytes.Buffer, items []T, members func(T) [2][2]string) error {
	if len(items) == 0 {
		b.WriteString("[]")
		return nil
	}
	b.WriteString("[\n")
	for i, item := range items {
		b.WriteString("        {")
		for j, member := range members(item) {
			if j > 0 {
				b.WriteString(", ")
			}
			for k, text := range member {
				if k > 0 {
					b.WriteString(": ")
				}
				if err := writeString(b, text); err != nil {
					return err
				}
			}
		}
		b.WriteString("}")
		if i < len(items)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("    ]")
	return nil
}

// writeString writes s as a JSON string, without escaping HTML.
func writeString(b *bytes.Buffer, s string) error {
	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	b.Write(bytes.TrimSuffix(raw.Bytes(), []byte("\n")))
	return nil
}
