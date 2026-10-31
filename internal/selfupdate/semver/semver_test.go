package semver

import "testing"

func TestCompareOrdersStableVersions(t *testing.T) {
	current, ok := Parse("v2.9.10+build.1")
	if !ok {
		t.Fatal("current version did not parse")
	}
	latest, ok := Parse("2.10.0")
	if !ok || Compare(current, latest) >= 0 {
		t.Fatalf("comparison current=%v latest=%v ok=%v", current, latest, ok)
	}
	if Compare(latest, current) <= 0 || Compare(latest, latest) != 0 {
		t.Fatal("Compare must be antisymmetric and reflexive")
	}
}

func TestParseRejectsNonStableVersions(t *testing.T) {
	for _, raw := range []string{"", "dev", "edge-abcdef0", "build-abcdef0", "v1.2", "1.2", "v1.2.3.4", "v1.2.3-rc.1", "v1..3", "v1.x.3", "v+1.2.3"} {
		if _, ok := Parse(raw); ok {
			t.Fatalf("%q must not parse as a stable version", raw)
		}
	}
}

func TestParseAcceptsMetadataAndMissingPrefix(t *testing.T) {
	for raw, want := range map[string]Version{
		"v1.2.3":      {1, 2, 3},
		"1.2.3":       {1, 2, 3},
		" v1.2.3 ":    {1, 2, 3},
		"v1.2.3+meta": {1, 2, 3},
		// 保持既有实现语义：前导零按十进制解析，不视为非法。
		"v01.2.3": {1, 2, 3},
	} {
		got, ok := Parse(raw)
		if !ok || got != want {
			t.Fatalf("Parse(%q)=%v,%v want %v", raw, got, ok, want)
		}
	}
}

func TestNormalizeAndString(t *testing.T) {
	if Normalize("1.2.3") != "v1.2.3" || Normalize("v1.2.3") != "v1.2.3" || Normalize(" ") != "" {
		t.Fatal("Normalize must add a single v prefix and keep empty values")
	}
	if (Version{1, 20, 3}).String() != "v1.20.3" {
		t.Fatal("String must render the canonical form")
	}
}

func TestLess(t *testing.T) {
	less, err := Less("v0.9.9", "v0.10.0")
	if err != nil || !less {
		t.Fatalf("Less=%v err=%v", less, err)
	}
	if _, err := Less("dev", "v0.1.0"); err == nil {
		t.Fatal("non-stable input must return an error")
	}
}
