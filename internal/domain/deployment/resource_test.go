package deployment

import (
	"strings"
	"testing"
)

func TestNewPostgresResource_DerivesNamesFromApplicationID(t *testing.T) {
	r, err := NewPostgresResource("orders-v3", "medium", "16", 65536)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := r.Name(), "orders-v3"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := r.DatabaseName(), "orders-v3-db"; got != want {
		t.Errorf("DatabaseName() = %q, want %q", got, want)
	}
	if r.Type() != ResourceTypePostgres {
		t.Errorf("Type() = %q, want %q", r.Type(), ResourceTypePostgres)
	}
	if r.Size() != "medium" || r.Version() != "16" || r.StorageMb() != 65536 {
		t.Errorf("shape = %s/%s/%d, want medium/16/65536", r.Size(), r.Version(), r.StorageMb())
	}
}

func TestNewPostgresResource_Defaults(t *testing.T) {
	r, err := NewPostgresResource("orders", "", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Size() != DefaultPostgresSize {
		t.Errorf("Size() = %q, want %q", r.Size(), DefaultPostgresSize)
	}
	if r.Version() != DefaultPostgresVersion {
		t.Errorf("Version() = %q, want %q", r.Version(), DefaultPostgresVersion)
	}
	if r.StorageMb() != DefaultPostgresStorageMb {
		t.Errorf("StorageMb() = %d, want %d", r.StorageMb(), DefaultPostgresStorageMb)
	}
}

func TestNewPostgresResource_RejectsWhatTheXRDWouldReject(t *testing.T) {
	tests := []struct {
		name      string
		appID     string
		size      string
		version   string
		storageMb int
		wantErr   string
	}{
		{"size off the enum", "orders", "xlarge", "16", 32768, "not one of small|medium|large"},
		{"version off the enum", "orders", "small", "13", 32768, "not one of 14|15|16"},
		{"storage below the XRD minimum", "orders", "small", "16", 1024, "outside the supported range"},
		{"storage above the XRD maximum", "orders", "small", "16", 99999999, "outside the supported range"},
		{"empty application id", "", "small", "16", 32768, "application ID is required"},
		{"application id is not an RFC 1123 label", "Orders_V3", "small", "16", 32768, "RFC 1123 label"},
		{"application id starts with a hyphen", "-orders", "small", "16", 32768, "RFC 1123 label"},
		{
			"application id longer than the Azure server name allows",
			strings.Repeat("a", maxResourceNameLen+1),
			"small", "16", 32768,
			"must be at most 50",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPostgresResource(tc.appID, tc.size, tc.version, tc.storageMb)
			if err == nil {
				t.Fatalf("expected an error, got none")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// The 50-character bound is the Azure Flexible Server name limit minus the
// uniquifier the composed managed resource adds, so exactly 50 must pass — an
// off-by-one here silently rejects legitimate applications.
func TestNewPostgresResource_MaxLengthBoundaryIsInclusive(t *testing.T) {
	if _, err := NewPostgresResource(strings.Repeat("a", maxResourceNameLen), "small", "16", 32768); err != nil {
		t.Fatalf("a %d-character application ID must be accepted: %v", maxResourceNameLen, err)
	}
}

func TestNewResource_TypeDispatch(t *testing.T) {
	if _, err := NewResource("postgres", "orders", "", "", 0); err != nil {
		t.Fatalf("postgres must be supported: %v", err)
	}
	if _, err := NewResource("", "orders", "", "", 0); err == nil {
		t.Error("an empty type must be rejected")
	}
	// Cache is specced but unimplemented (ADR-0019). It must refuse rather than
	// accept a deployment that silently omits the dependency.
	_, err := NewResource("cache", "orders", "", "", 0)
	if err == nil {
		t.Fatal("an unimplemented type must be rejected")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %q, want it to name the type as unsupported", err.Error())
	}
}
