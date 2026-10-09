package permission_table

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/donnyhardyanto/dxlib/utils"
)

const wantTable = `{
    "grants": [
        {"role": "ADMIN", "permission": "EVERYTHING"},
        {"role": "CLERK", "permission": "ORDER.CREATE"},
        {"role": "CLERK", "permission": "ORDER.LIST"},
        {"role": "VIEWER", "permission": "ORDER.LIST"}
    ],
    "gates": [
        {"check": "ApiKeyCheck", "setting": "API_KEY"},
        {"check": "ApiKeyCheck", "setting": "API_KEY_2"}
    ]
}
`

func roleRows() []utils.JSON {
	return []utils.JSON{
		{"id": int64(3), "nameid": "VIEWER"},
		{"id": int64(1), "nameid": "ADMIN"},
		{"id": int64(2), "nameid": "CLERK"},
		{"id": int64(4), "nameid": "UNUSED"},
	}
}

func rolePrivilegeRows() []utils.JSON {
	return []utils.JSON{
		{"role_id": int64(2), "privilege_nameid": "ORDER.LIST"},
		{"role_id": int64(3), "privilege_nameid": "ORDER.LIST"},
		{"role_id": int64(1), "privilege_nameid": "EVERYTHING"},
		{"role_id": int64(2), "privilege_nameid": "ORDER.CREATE"},
		{"role_id": int64(2), "privilege_nameid": "ORDER.LIST"},
	}
}

func write(t *testing.T, grants []Grant, gates []Gate) string {
	t.Helper()
	var b bytes.Buffer
	if err := Write(&b, grants, gates); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return b.String()
}

func TestWriteFromRows(t *testing.T) {
	grants, err := GrantsFromRows(roleRows(), rolePrivilegeRows())
	if err != nil {
		t.Fatalf("GrantsFromRows: %v", err)
	}
	gates := []Gate{
		{Check: "ApiKeyCheck", Setting: "API_KEY_2"},
		{Check: "ApiKeyCheck", Setting: "API_KEY"},
		{Check: "ApiKeyCheck", Setting: "API_KEY_2"},
	}
	got := write(t, grants, gates)
	if got != wantTable {
		t.Fatalf("table:\n%s\nwant:\n%s", got, wantTable)
	}

	// The same rows in another order give the same bytes.
	roles, privileges := roleRows(), rolePrivilegeRows()
	for i, j := 0, len(roles)-1; i < j; i, j = i+1, j-1 {
		roles[i], roles[j] = roles[j], roles[i]
	}
	for i, j := 0, len(privileges)-1; i < j; i, j = i+1, j-1 {
		privileges[i], privileges[j] = privileges[j], privileges[i]
	}
	grants, err = GrantsFromRows(roles, privileges)
	if err != nil {
		t.Fatalf("GrantsFromRows: %v", err)
	}
	if again := write(t, grants, []Gate{gates[1], gates[0]}); again != got {
		t.Fatalf("order changed the table:\n%s", again)
	}
}

func TestWriteEmpty(t *testing.T) {
	got := write(t, nil, ModuleGates())
	want := "{\n    \"grants\": [],\n    \"gates\": []\n}\n"
	if got != want {
		t.Fatalf("table %q, want %q", got, want)
	}
}

func TestWriteIsTheTableFormat(t *testing.T) {
	got := write(t, []Grant{{Role: `A "<b>"`, Permission: "P&Q"}}, nil)
	if first := strings.SplitN(got, "\n", 2)[0]; first != "{" {
		t.Fatalf("first line %q, want {", first)
	}
	if !strings.Contains(got, `{"role": "A \"<b>\"", "permission": "P&Q"}`) {
		t.Fatalf("names not written as JSON strings without HTML escapes:\n%s", got)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &keys); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(keys) != 2 || keys["grants"] == nil || keys["gates"] == nil {
		t.Fatalf("keys %v, want grants and gates only", keys)
	}
	var table struct {
		Grants []Grant `json:"grants"`
		Gates  []Gate  `json:"gates"`
	}
	if err := json.Unmarshal([]byte(got), &table); err != nil {
		t.Fatalf("not a table: %v", err)
	}
	if len(table.Grants) != 1 || table.Grants[0].Role != `A "<b>"` || table.Gates == nil {
		t.Fatalf("read back %+v", table)
	}
}

func TestWriteRefusesEmptyNames(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, []Grant{{Role: "A"}}, nil); err == nil {
		t.Fatal("a grant with no permission was written")
	}
	if err := Write(&b, nil, []Gate{{Check: "Check"}}); err == nil {
		t.Fatal("a gate with no setting was written")
	}
	if b.Len() != 0 {
		t.Fatalf("wrote %q before refusing", b.String())
	}
}

func TestGrantsFromRowsRefusesUnknownRole(t *testing.T) {
	_, err := GrantsFromRows(roleRows(), []utils.JSON{{"role_id": int64(9), "privilege_nameid": "ORDER.LIST"}})
	if err == nil {
		t.Fatal("a grant of a role not in the role table was named")
	}
}
