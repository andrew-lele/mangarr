package registry

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeTestFile(t *testing.T, name, content string) string {
	root := t.TempDir()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadGroupsMissingFileReturnsEmptyRegistry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry, err := LoadGroups(root)
	if err != nil {
		t.Fatalf("LoadGroups returned error for missing file: %v", err)
	}
	if len(registry.Groups) != 0 {
		t.Fatalf("expected empty groups, got %v", registry.Groups)
	}
}

func TestLoadGroupsParsesEntryAndBuildsIndexes(t *testing.T) {
	t.Parallel()

	yaml := `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "CP", "Comic Zen" ]
    sources:
      comix: "9641"

  b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c:
    aliases: [ "Asura Scans" ]
    sources:
      comix: "4725"
`
	root := writeTestFile(t, "groups.yaml", yaml)

	registry, err := LoadGroups(root)
	if err != nil {
		t.Fatalf("LoadGroups error: %v", err)
	}

	cp := registry.Groups["a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b"]
	if cp == nil {
		t.Fatal("CP group not found by canonical id")
	}
	if !slices.Contains(cp.Aliases, "CP") {
		t.Fatalf("CP aliases = %v, want CP", cp.Aliases)
	}

	// Alias -> canonical id
	if got := registry.AliasIndex["cp"]; got != "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b" {
		t.Fatalf("alias index cp = %q, want canonical id", got)
	}

	// Native id -> canonical id
	if got := registry.NativeIndex["comix:4725"]; got != "b2ec9d4f-5a01-4d63-8c8b-8e3f5d2b0a4c" {
		t.Fatalf("native index comix:4725 = %q, want asura id", got)
	}
}

func TestResolveGroupIDAcceptsAliasAndNativeKey(t *testing.T) {
	t.Parallel()

	yaml := `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "CP" ]
    sources:
      comix: "9641"
`
	root := writeTestFile(t, "groups.yaml", yaml)
	registry, _ := LoadGroups(root)

	if got := ResolveGroupID(&registry, "CP"); got != "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b" {
		t.Fatalf("ResolveGroupID(CP) = %q, want canonical id", got)
	}
	if got := ResolveGroupID(&registry, "comix:9641"); got != "a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b" {
		t.Fatalf("ResolveGroupID(comix:9641) = %q, want canonical id", got)
	}
	if got := ResolveGroupID(&registry, "unknown"); got != "" {
		t.Fatalf("ResolveGroupID(unknown) = %q, want empty", got)
	}
}

func writeFile(t *testing.T, root, name, content string) {
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProfilesValidatesGroupReferences(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "groups.yaml", `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "CP" ]
    sources:
      comix: "9641"
`)
	groups, _ := LoadGroups(root)

	writeFile(t, root, "profiles.yaml", `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Comix Preferred"
    preferredGroups: [ "CP" ]
    ignoredGroups: [ ]
    fallback: "any"
`)
	profiles, err := LoadProfiles(root, &groups)
	if err != nil {
		t.Fatalf("LoadProfiles error: %v", err)
	}
	if !HasProfile(&profiles, "Comix Preferred") {
		t.Fatalf("profile registry missing Comix Preferred: %v", profiles.Profiles)
	}
}

func TestLoadProfilesRejectsUnknownGroupReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "groups.yaml", `version: 1

groups:
  a1fdb8c3-4e90-4c52-9b7a-7d2e4c1a9f3b:
    aliases: [ "CP" ]
    sources:
      comix: "9641"
`)
	groups, _ := LoadGroups(root)

	writeFile(t, root, "profiles.yaml", `version: 1

profiles:
  f47ac10b-58b9-4b56-8b4d-8e0c3d5e9a2f:
    name: "Comix Preferred"
    preferredGroups: [ "DoesNotExist" ]
    ignoredGroups: [ ]
    fallback: "any"
`)
	_, err := LoadProfiles(root, &groups)
	if err == nil {
		t.Fatal("LoadProfiles accepted unknown group reference")
	}
}