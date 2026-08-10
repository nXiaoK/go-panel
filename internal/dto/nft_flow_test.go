package dto

import "testing"

func TestNftFlowBatchDigestIsCanonicalAndContentBound(t *testing.T) {
	a := NftFlowBatchV2Dto{
		ReporterID: "reporter", Sequence: 1, BatchID: "batch",
		CapturedAt: 1_754_092_800_123,
		Items:      []NftFlowItem{digestItem(2, 3, 4, 5, 6), digestItem(1, 2, 3, 4, 5)},
	}
	b := a
	b.Items = []NftFlowItem{a.Items[1], a.Items[0]}
	digestA, err := NftFlowBatchDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := NftFlowBatchDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB {
		t.Fatalf("item order changed digest: %s / %s", digestA, digestB)
	}
	b.Items[0] = digestItem(1, 2, 3, 4, 99)
	changed, err := NftFlowBatchDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if changed == digestA {
		t.Fatal("changed delta did not change digest")
	}
	b = a
	b.CapturedAt++
	changed, err = NftFlowBatchDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if changed == digestA {
		t.Fatal("changed capture time did not change digest")
	}
}

func TestNftFlowBatchDigestKeepsLegacyZeroCaptureDigest(t *testing.T) {
	batch := NftFlowBatchV2Dto{
		ReporterID: "reporter", Sequence: 1, BatchID: "batch",
		Items: []NftFlowItem{digestItem(1, 2, 3, 4, 5)},
	}
	digest, err := NftFlowBatchDigest(batch)
	if err != nil {
		t.Fatal(err)
	}
	// 固定旧协议摘要，防止可选采集时间让已持久化批次无法匹配面板 ACK。
	const legacyDigest = "02514ff8a044adbb616cf39e22ce8cfee5bad51e667e7c4ae8cfd9fe5a241848"
	if digest != legacyDigest {
		t.Fatalf("legacy digest=%s, want %s", digest, legacyDigest)
	}
}

func digestItem(forwardID, userID, userTunnelID, up, down int64) NftFlowItem {
	return NftFlowItem{ForwardID: &forwardID, UserID: &userID, UserTunnelID: &userTunnelID, Up: &up, Down: &down}
}
