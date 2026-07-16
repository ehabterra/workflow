package yaml_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteBuilderDBProviderError(t *testing.T) {
	builder := yaml.NewSQLiteStorageBuilder(func() (*sql.DB, error) {
		return nil, errors.New("provider boom")
	})
	if _, _, err := builder.Build(map[string]any{}); err == nil {
		t.Fatal("Build should surface a DBProvider error")
	}
}

func TestSQLiteBuilderFromConfigDatabase(t *testing.T) {
	// No DBProvider: the builder opens the connection from the config's
	// "database" key (":memory:" by default when absent).
	builder := yaml.NewSQLiteStorageBuilder(nil)
	store, init, err := builder.Build(map[string]any{"database": ":memory:", "table": "wf"})
	if err != nil {
		t.Fatalf("Build from config database = %v", err)
	}
	if store == nil || init == nil {
		t.Fatal("Build returned nil store/init")
	}
}

func TestSetupStorageFromConfigSQLite(t *testing.T) {
	if _, err := yaml.SetupStorageFromConfig(nil); err == nil {
		t.Fatal("SetupStorageFromConfig(nil) should error")
	}

	res, err := yaml.SetupStorageFromConfig(&yaml.StorageConfig{
		Type:   "sqlite",
		Config: map[string]any{"database": ":memory:", "table": "wf_setup"},
	})
	if err != nil {
		t.Fatalf("SetupStorageFromConfig(sqlite) = %v", err)
	}
	if res == nil || res.Storage == nil {
		t.Fatal("setup returned no storage")
	}

	// With a history sub-config, the setup also builds and initializes a history
	// store on the shared SQL connection.
	withHistory, err := yaml.SetupStorageFromConfig(&yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"database": ":memory:",
			"table":    "wf_setup2",
			"history": map[string]any{
				"table":         "audit_log",
				"custom_fields": map[string]any{"reason": "reason TEXT"},
			},
		},
	})
	if err != nil {
		t.Fatalf("SetupStorageFromConfig(sqlite+history) = %v", err)
	}
	if withHistory == nil || withHistory.HistoryStore == nil {
		t.Fatal("setup with history config returned no history store")
	}
}
