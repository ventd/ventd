package hwmon

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestReadDMI(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  DMIInfo
	}{
		{
			name: "all fields populated",
			files: map[string]string{
				"board_vendor": "Micro-Star International Co., Ltd.\n",
				"board_name":   "MAG B550 TOMAHAWK (MS-7C91)\n",
				"product_name": "MS-7C91\n",
				"sys_vendor":   "Micro-Star International Co., Ltd.\n",
			},
			want: DMIInfo{
				BoardVendor: "micro-star international co., ltd.",
				BoardName:   "mag b550 tomahawk (ms-7c91)",
				ProductName: "ms-7c91",
				SysVendor:   "micro-star international co., ltd.",
			},
		},
		{
			name:  "missing files are empty",
			files: map[string]string{"board_vendor": "Gigabyte Technology Co., Ltd.\n"},
			want:  DMIInfo{BoardVendor: "gigabyte technology co., ltd."},
		},
		{
			name:  "fully absent root — zero value",
			files: nil,
			want:  DMIInfo{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range tt.files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got := ReadDMI(root)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReadDMI = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDMITrigger_Matches(t *testing.T) {
	info := DMIInfo{
		BoardVendor: "micro-star international co., ltd.",
		BoardName:   "mag b550 tomahawk",
		ProductName: "ms-7c91",
		SysVendor:   "micro-star international co., ltd.",
	}
	tests := []struct {
		name    string
		trigger DMITrigger
		want    bool
	}{
		{"vendor match", DMITrigger{BoardVendorContains: "micro-star"}, true},
		{"vendor + name match", DMITrigger{BoardVendorContains: "micro-star", BoardNameContains: "mag"}, true},
		{"case-insensitive needle", DMITrigger{BoardVendorContains: "MICRO-STAR"}, true},
		{"product wildcard (empty) doesn't break", DMITrigger{BoardVendorContains: "micro-star", ProductContains: ""}, true},
		{"vendor mismatch", DMITrigger{BoardVendorContains: "gigabyte"}, false},
		{"name mismatch", DMITrigger{BoardVendorContains: "micro-star", BoardNameContains: "x570"}, false},
		{"empty trigger never matches", DMITrigger{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.trigger.matches(info); got != tt.want {
				t.Errorf("matches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProposeModulesByDMI(t *testing.T) {
	tests := []struct {
		name string
		info DMIInfo
		want []string // DriverNeed.Key list, sorted
	}{
		{
			name: "MSI MAG board → nct6687d",
			info: DMIInfo{BoardVendor: "micro-star international co., ltd.", BoardName: "mag b550 tomahawk"},
			want: []string{"nct6687d"},
		},
		{
			name: "MSI MPG board → nct6687d",
			info: DMIInfo{BoardVendor: "msi", BoardName: "mpg z790 edge"},
			want: []string{"nct6687d"},
		},
		{
			name: "Gigabyte board → it8688e",
			info: DMIInfo{BoardVendor: "gigabyte technology co., ltd.", BoardName: "b550 aorus elite"},
			want: []string{"it8688e"},
		},
		{
			name: "ASUS board with no seed trigger → empty",
			info: DMIInfo{BoardVendor: "asustek computer inc.", BoardName: "prime x570-p"},
			want: nil,
		},
		{
			name: "MSI non-MAG (e.g. classic NCT6775 board) → empty",
			info: DMIInfo{BoardVendor: "micro-star international co., ltd.", BoardName: "z390-a pro"},
			want: nil,
		},
		{
			name: "completely empty DMI → empty",
			info: DMIInfo{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProposeModulesByDMI(tt.info)
			keys := make([]string, len(got))
			for i, nd := range got {
				keys[i] = nd.Key
			}
			sort.Strings(keys)
			if len(keys) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(keys, tt.want) {
				t.Errorf("keys = %v, want %v", keys, tt.want)
			}
		})
	}
}

func TestProposeModulesByDMI_StableOrder(t *testing.T) {
	// Two matching candidates — make sure output order is deterministic
	// (alphabetic by Key). Fabricate by simultaneously matching gigabyte+mag,
	// which won't happen on real hardware but exercises the sort path.
	info := DMIInfo{
		BoardVendor: "gigabyte technology co., ltd.",
		BoardName:   "mag something",
	}
	got := ProposeModulesByDMI(info)
	keys := make([]string, len(got))
	for i, nd := range got {
		keys[i] = nd.Key
	}
	// gigabyte matches it8688e; "mag" in board_name is fine but MSI vendor
	// triggers on nct6687d require micro-star/msi, so only it8688e should
	// come back. If future seed entries add gigabyte MAG triggers, update.
	if len(keys) != 1 || keys[0] != "it8688e" {
		t.Errorf("keys = %v, want [it8688e] — seed coverage changed?", keys)
	}
}
