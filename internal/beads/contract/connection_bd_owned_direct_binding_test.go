package contract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

// ScopeCarriesBdOwnedDirectBinding is what keeps gc's boot-time canonicalizer
// off a scope bd owns. The stamp that canonicalizer would otherwise write —
// `gc.endpoint_origin` — is the very discriminator ResolveDoltConnectionTarget's
// bd-owned fallbacks rest on, so a scope carrying bd's binding and none of gc's
// endpoint keys has to be recognized before anything writes to it.
func TestScopeCarriesBdOwnedDirectBinding(t *testing.T) {
	fs := fsys.OSFS{}
	for name, tc := range map[string]struct {
		metadata string
		config   func(t *testing.T, scopeRoot string)
		want     bool
	}{
		"bd template config and an external binding": {
			metadata: bdExternalTCPMetadata,
			config:   func(t *testing.T, scopeRoot string) { writeBdTemplateConfig(t, fs, scopeRoot, "gc") },
			want:     true,
		},
		"no config at all": {
			metadata: bdExternalTCPMetadata,
			want:     true,
		},
		// The gc-managed legacy non-regression: bd's `init --server` records
		// whichever server it was pointed at, gc's own managed one included, so
		// a gc-managed legacy scope carries a binding too. The host is what
		// tells the two apart, and that scope must still be canonicalised.
		"loopback binding is gc's own managed server": {
			metadata: `{"database":"dolt","backend":"dolt","dolt_mode":"server",` +
				`"dolt_server_host":"127.0.0.1","dolt_server_port":3307,"dolt_database":"hq"}`,
			config: func(t *testing.T, scopeRoot string) { writeBdTemplateConfig(t, fs, scopeRoot, "gc") },
			want:   false,
		},
		"socket binding names this host": {
			metadata: `{"database":"dolt","backend":"dolt","dolt_mode":"server",` +
				`"dolt_server_socket":"/var/run/dolt.sock","dolt_database":"hosted"}`,
			config: func(t *testing.T, scopeRoot string) { writeBdTemplateConfig(t, fs, scopeRoot, "gc") },
			want:   false,
		},
		"no binding at all": {
			metadata: `{"database":"dolt","backend":"dolt","dolt_mode":"server","dolt_database":"hq"}`,
			config:   func(t *testing.T, scopeRoot string) { writeBdTemplateConfig(t, fs, scopeRoot, "gc") },
			want:     false,
		},
		"gc-authoritative config outranks the binding": {
			metadata: bdExternalTCPMetadata,
			config: func(t *testing.T, scopeRoot string) {
				if _, err := EnsureCanonicalConfig(fs, filepath.Join(scopeRoot, ".beads", "config.yaml"), ConfigState{
					IssuePrefix:    "gc",
					EndpointOrigin: EndpointOriginManagedCity,
					EndpointStatus: EndpointStatusVerified,
					DoltMode:       "server",
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			city := t.TempDir()
			writeRawMetadata(t, fs, city, tc.metadata)
			if tc.config != nil {
				tc.config(t, city)
			}
			got, err := ScopeCarriesBdOwnedDirectBinding(fs, city, city, "gc")
			if err != nil {
				t.Fatalf("ScopeCarriesBdOwnedDirectBinding(): %v", err)
			}
			if got != tc.want {
				t.Fatalf("ScopeCarriesBdOwnedDirectBinding() = %v, want %v", got, tc.want)
			}
		})
	}
}

// GC_DOLT_HOST redirects gc's own managed target away from loopback, so a
// binding naming that host is still a record of gc's server, not bd's upstream.
func TestScopeCarriesBdOwnedDirectBindingIgnoresTheManagedHostOverride(t *testing.T) {
	fs := fsys.OSFS{}
	t.Setenv(ManagedCityHostEnv, "dolt.internal")
	city := t.TempDir()
	writeBdTemplateConfig(t, fs, city, "gc")
	writeRawMetadata(t, fs, city, `{"database":"dolt","backend":"dolt","dolt_mode":"server",`+
		`"dolt_server_host":"dolt.internal","dolt_server_port":3307,"dolt_database":"hq"}`)

	got, err := ScopeCarriesBdOwnedDirectBinding(fs, city, city, "gc")
	if err != nil {
		t.Fatalf("ScopeCarriesBdOwnedDirectBinding(): %v", err)
	}
	if got {
		t.Fatalf("a binding naming gc's own managed host classified as bd-owned")
	}
}

func TestScopeMetadataNamesForeignDoltServer(t *testing.T) {
	t.Setenv(ManagedCityHostEnv, "dolt.internal")
	for _, tc := range []struct {
		name     string
		metadata string
		want     bool
	}{
		{name: "host only", metadata: `{"dolt_mode":"server","dolt_server_host":"dolt"}`, want: true},
		{name: "with port", metadata: `{"dolt_mode":"server","dolt_server_host":"dolt","dolt_server_port":3307}`, want: true},
		{name: "normalized fields", metadata: `{"dolt_mode":" SERVER ","dolt_server_host":" db.example "}`, want: true},
		{name: "socket", metadata: `{"dolt_mode":"server","dolt_server_host":"dolt","dolt_server_socket":"/var/run/dolt.sock"}`},
		{name: "loopback", metadata: `{"dolt_mode":"server","dolt_server_host":"127.0.0.1"}`},
		{name: "localhost", metadata: `{"dolt_mode":"server","dolt_server_host":"localhost"}`},
		{name: "IPv6 loopback", metadata: `{"dolt_mode":"server","dolt_server_host":"[::1]"}`},
		{name: "unspecified", metadata: `{"dolt_mode":"server","dolt_server_host":"0.0.0.0"}`},
		{name: "managed host", metadata: `{"dolt_mode":"server","dolt_server_host":"DOLT.INTERNAL"}`},
		{name: "missing host", metadata: `{"dolt_mode":"server"}`},
		{name: "missing mode", metadata: `{"dolt_server_host":"dolt"}`},
		{name: "proxied mode", metadata: `{"dolt_mode":"proxied-server","dolt_server_host":"dolt"}`},
		{name: "missing metadata"},
		{name: "malformed metadata", metadata: `{"dolt_mode":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := fsys.NewFake()
			const scope = "/city"
			if tc.metadata != "" {
				fs.Files[filepath.Join(scope, ".beads", "metadata.json")] = []byte(tc.metadata)
			}
			got, err := ScopeMetadataNamesForeignDoltServer(fs, scope)
			if err != nil {
				t.Fatalf("ScopeMetadataNamesForeignDoltServer: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ScopeMetadataNamesForeignDoltServer = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateInheritedCityEndpointMirrorAcceptsBdOwnedDirectCity accepts inheritance without a gc endpoint mirror.
func TestValidateInheritedCityEndpointMirrorAcceptsBdOwnedDirectCity(t *testing.T) {
	fs := fsys.OSFS{}
	city := t.TempDir()
	rig := filepath.Join(city, "rigs", "frontend")
	writeRawMetadata(t, fs, city, `{"database":"dolt","backend":"dolt","dolt_mode":"server","dolt_server_host":"db.example.test","dolt_server_port":3306,"dolt_server_user":"root","dolt_database":"gc_hq"}`)
	if err := os.WriteFile(filepath.Join(city, ".beads", "config.yaml"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rig, ".beads"), 0o700); err != nil {
		t.Fatal(err)
	}
	rigConfig := "issue_prefix: pl\ngc.endpoint_origin: inherited_city\ngc.endpoint_status: verified\ndolt.mode: server\ndolt.auto-start: false\n"
	if err := os.WriteFile(filepath.Join(rig, ".beads", "config.yaml"), []byte(rigConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	writeCanonicalMetadata(t, fs, rig, "pl")

	if err := ValidateInheritedCityEndpointMirror(fs, city, rig); err != nil {
		t.Fatalf("ValidateInheritedCityEndpointMirror() = %v, want no drift for a bd-owned direct city", err)
	}
}
