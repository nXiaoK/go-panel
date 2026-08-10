package buildinfo

import "testing"

func TestCurrentNormalizesInjectedValues(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, Commit, BuildTime
	t.Cleanup(func() {
		Version, Commit, BuildTime = oldVersion, oldCommit, oldBuildTime
	})

	Version = " v1.2.3 "
	Commit = " abc1234 "
	BuildTime = " "
	got := Current()
	if got.Version != "v1.2.3" || got.Commit != "abc1234" || got.BuildTime != "unknown" {
		t.Fatalf("Current() = %#v", got)
	}
	if got.SourceURL != "https://github.com/nXiaoK/go-panel" {
		t.Fatalf("SourceURL = %q", got.SourceURL)
	}
}
