package yaml_test

import (
	"strings"
	"testing"

	"github.com/ehabterra/workflow/yaml"
)

func TestLoadConfigFileErrors(t *testing.T) {
	if _, err := yaml.LoadConfig("/no/such/file.yaml"); err == nil {
		t.Fatal("LoadConfig of a missing file should error")
	}
	// Malformed YAML (a stray unknown key) is rejected by strict decoding.
	if _, err := yaml.LoadConfigFromBytes([]byte("workflow:\n  bogus_key: 1\n")); err == nil {
		t.Fatal("LoadConfigFromBytes should reject unknown keys")
	}
	if _, err := yaml.LoadConfigFromBytes([]byte(": : not yaml : :")); err == nil {
		t.Fatal("LoadConfigFromBytes should reject invalid YAML")
	}
}

func TestConfigValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing name",
			yaml: "workflow:\n  transitions:\n    - name: t\n      from: [a]\n      to: [b]\n",
			want: "name is required",
		},
		{
			name: "no transitions",
			yaml: "workflow:\n  name: w\n  initial_marking: a\n",
			want: "at least one transition",
		},
		{
			name: "undefined initial place",
			yaml: "workflow:\n  name: w\n  initial_marking: ghost\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a]\n      to: [b]\n",
			want: "undefined place",
		},
		{
			name: "empty from",
			yaml: "workflow:\n  name: w\n  initial_marking: a\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: []\n      to: [b]\n",
			want: "at least one 'from'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := yaml.LoadConfigFromBytes([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestInitialMarkingDecodeErrors(t *testing.T) {
	// Sequence with a non-string element fails to decode into []string.
	seqBad := "workflow:\n  name: w\n  initial_marking:\n    - a\n    - {x: 1}\n  places:\n    - name: a\n  transitions:\n    - name: t\n      from: [a]\n      to: [a]\n"
	if _, err := yaml.LoadConfigFromBytes([]byte(seqBad)); err == nil {
		t.Fatal("a sequence initial_marking with a mapping element should error")
	}
	// Mapping whose value is not a token list fails to decode.
	mapBad := "workflow:\n  name: w\n  initial_marking:\n    a: not-a-token-list\n  places:\n    - name: a\n  transitions:\n    - name: t\n      from: [a]\n      to: [a]\n"
	if _, err := yaml.LoadConfigFromBytes([]byte(mapBad)); err == nil {
		t.Fatal("a mapping initial_marking with a scalar value should error")
	}
}

func TestConfigValidateToAndResetPlaces(t *testing.T) {
	// 'to' references an undefined place.
	toBad := "workflow:\n  name: w\n  initial_marking: a\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a]\n      to: [ghost]\n"
	if _, err := yaml.LoadConfigFromBytes([]byte(toBad)); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("undefined 'to' place err = %v, want mention of 'ghost'", err)
	}
	// 'resets' references an undefined place.
	resetBad := "workflow:\n  name: w\n  initial_marking: a\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a]\n      to: [b]\n      resets: [ghost]\n"
	if _, err := yaml.LoadConfigFromBytes([]byte(resetBad)); err == nil {
		t.Fatal("undefined 'resets' place should error")
	}
}

func TestStorageFactoryErrors(t *testing.T) {
	f := yaml.NewStorageFactory()

	if _, _, err := f.Build(nil); err == nil {
		t.Fatal("Build(nil) should error")
	}
	if _, _, err := f.Build(&yaml.StorageConfig{}); err == nil {
		t.Fatal("Build with empty type should error")
	}
	if _, _, err := f.Build(&yaml.StorageConfig{Type: "does-not-exist"}); err == nil {
		t.Fatal("Build with unknown type should error")
	}

	// Register(nil) panics by contract.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Register(nil) should panic")
		}
	}()
	f.Register(nil)
}
