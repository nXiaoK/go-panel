package gost

import "testing"

func TestUpgradeNodeCommandDataCarriesAllowInsecureExplicitly(t *testing.T) {
	for _, allow := range []bool{false, true} {
		data := upgradeNodeCommandData("https://panel.example.com/", "nftables", "1.2.3", allow)
		got, ok := data["allowInsecure"]
		if !ok {
			t.Fatal("upgrade command data omitted allowInsecure")
		}
		if got != allow {
			t.Fatalf("allowInsecure = %#v, want %v", got, allow)
		}
		if data["baseUrl"] != "https://panel.example.com" {
			t.Fatalf("baseUrl = %#v", data["baseUrl"])
		}
	}
}
