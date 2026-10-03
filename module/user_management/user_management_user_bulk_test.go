package user_management

import "testing"

func TestBulkUserColumnAsInt64(t *testing.T) {
	cases := []struct {
		row     map[string]interface{}
		want    int64
		wantSet bool
		wantErr bool
	}{
		{map[string]interface{}{}, 0, false, false},
		{map[string]interface{}{"role_id": float64(7)}, 7, true, false}, // XLSX
		{map[string]interface{}{"role_id": "7"}, 7, true, false},        // CSV
		{map[string]interface{}{"role_id": " 12 "}, 12, true, false},
		{map[string]interface{}{"role_id": "admin"}, 0, false, true},
	}
	for _, c := range cases {
		got, isSet, err := bulkUserColumnAsInt64(c.row, "role_id")
		if (err != nil) != c.wantErr || got != c.want || isSet != c.wantSet {
			t.Errorf("%v: got (%d, %v, %v), want (%d, %v, err=%v)", c.row, got, isSet, err, c.want, c.wantSet, c.wantErr)
		}
	}
}
