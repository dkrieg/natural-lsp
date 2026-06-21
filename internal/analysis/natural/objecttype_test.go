package natural

import (
	"natural-lsp/internal/model"
	"testing"
)

// TestClassify exercises the classify function's core behavior per Task 2 acceptance criteria.
// Covers case-insensitivity, built-in table lookup, custom override maps, and unknown extensions
// (FR-7, FR-9).
func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		custom   map[string]model.ObjectType
		expected model.ObjectType
	}{
		// Core types: lowercase input
		{"NSP_lowercase", "customer.nsp", nil, model.ObjectProgram},
		{"NSN_lowercase", "sub.nsn", nil, model.ObjectSubprogram},
		{"NSS_lowercase", "routine.nss", nil, model.ObjectExternalSubroutine},
		{"NSC_lowercase", "shared.nsc", nil, model.ObjectCopycode},
		{"NSM_lowercase", "screen.nsm", nil, model.ObjectMap},
		{"NSL_lowercase", "local.nsl", nil, model.ObjectLocalDataArea},
		{"NSG_lowercase", "global.nsg", nil, model.ObjectGlobalDataArea},
		{"NSA_lowercase", "param.nsa", nil, model.ObjectParameterDataArea},
		{"NSH_lowercase", "help.nsh", nil, model.ObjectHelproutine},
		{"NSD_lowercase", "data.nsd", nil, model.ObjectDDM},

		// Extended types: lowercase input
		{"NS4_lowercase", "myclass.ns4", nil, model.ObjectClass},
		{"NS7_lowercase", "myfunc.ns7", nil, model.ObjectFunction},
		{"NS3_lowercase", "mydialog.ns3", nil, model.ObjectDialog},
		{"NS8_lowercase", "myadapter.ns8", nil, model.ObjectAdapter},
		{"NST_lowercase", "readme.nst", nil, model.ObjectText},

		// Core types: uppercase input
		{"NSP_uppercase", "CUSTOMER.NSP", nil, model.ObjectProgram},
		{"NSN_uppercase", "SUB.NSN", nil, model.ObjectSubprogram},
		{"NSS_uppercase", "ROUTINE.NSS", nil, model.ObjectExternalSubroutine},
		{"NSC_uppercase", "SHARED.NSC", nil, model.ObjectCopycode},
		{"NSM_uppercase", "SCREEN.NSM", nil, model.ObjectMap},
		{"NSL_uppercase", "LOCAL.NSL", nil, model.ObjectLocalDataArea},
		{"NSG_uppercase", "GLOBAL.NSG", nil, model.ObjectGlobalDataArea},
		{"NSA_uppercase", "PARAM.NSA", nil, model.ObjectParameterDataArea},
		{"NSH_uppercase", "HELP.NSH", nil, model.ObjectHelproutine},
		{"NSD_uppercase", "DATA.NSD", nil, model.ObjectDDM},

		// Extended types: uppercase input
		{"NS4_uppercase", "MYCLASS.NS4", nil, model.ObjectClass},
		{"NS7_uppercase", "MYFUNC.NS7", nil, model.ObjectFunction},
		{"NS3_uppercase", "MYDIALOG.NS3", nil, model.ObjectDialog},
		{"NS8_uppercase", "MYADAPTER.NS8", nil, model.ObjectAdapter},
		{"NST_uppercase", "README.NST", nil, model.ObjectText},

		// Mixed case
		{"NSP_mixedcase", "File.NsP", nil, model.ObjectProgram},
		{"NSN_mixedcase", "Sub.NsN", nil, model.ObjectSubprogram},

		// Path with directory components
		{"with_dir_NSP", "lib/SRC/customer.nsp", nil, model.ObjectProgram},
		{"with_dir_NSN", "/absolute/path/sub.nsn", nil, model.ObjectSubprogram},

		// Unknown extensions
		{"unknown_md", "README.md", nil, model.ObjectUnknown},
		{"unknown_txt", "data.txt", nil, model.ObjectUnknown},
		{"unknown_nsx", "unknown.nsx", nil, model.ObjectUnknown},
		{"noext", "noextension", nil, model.ObjectUnknown},
		{"empty_string", "", nil, model.ObjectUnknown},

		// Custom override: map unknown to built-in type
		{"custom_nat_to_program", "file.nat", map[string]model.ObjectType{".NAT": model.ObjectProgram}, model.ObjectProgram},

		// Custom override: override built-in type
		{"custom_override_nsp", "file.nsp", map[string]model.ObjectType{".NSP": model.ObjectSubprogram}, model.ObjectSubprogram},

		// nil custom map behaves like built-in
		{"nil_custom", "file.nsp", nil, model.ObjectProgram},

		// empty custom map behaves like built-in
		{"empty_custom", "file.nsp", map[string]model.ObjectType{}, model.ObjectProgram},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.path, tc.custom)
			if got != tc.expected {
				t.Errorf("classify(%q, %v) = %q, want %q", tc.path, tc.custom, got, tc.expected)
			}
		})
	}
}
