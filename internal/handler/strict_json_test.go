package handler

import (
	"strings"
	"testing"
)

func TestDecodeStrictJSONObjectRejectsAmbiguousDocuments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "duplicate nested object key", raw: `{"items":[{"up":1,"up":2}]}`},
		{name: "duplicate object key nested in array", raw: `{"outer":[{"nested":{"down":1,"down":2}}]}`},
		{name: "unknown top-level field", raw: `{"reporterId":"r","unknown":1}`},
		{name: "unknown nested field", raw: `{"reporterId":"r","items":[{"mystery":1}]}`},
		{name: "trailing document", raw: `{"reporterId":"r"} {}`},
		{name: "non-object top level", raw: `[]`},
		{name: "empty document", raw: ``},
		{name: "malformed object", raw: `{"reporterId":"r"`},
		{name: "malformed close", raw: `{"reporterId":"r"]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var dst struct {
				ReporterID string `json:"reporterId"`
				Items      []struct {
					Up   int64 `json:"up"`
					Down int64 `json:"down"`
				} `json:"items"`
			}
			if err := decodeStrictJSONObject([]byte(tc.raw), &dst); err == nil {
				t.Fatalf("decodeStrictJSONObject(%q) succeeded", tc.raw)
			}
		})
	}
}

func TestDecodeStrictJSONObjectAllowsRepeatedStringValuesAndNestedContainers(t *testing.T) {
	raw := `{"reporterId":"same","items":[{"label":"same","up":1},{"label":"same","up":2}],"metadata":{"label":"same"}}`
	var dst struct {
		ReporterID string `json:"reporterId"`
		Items      []struct {
			Label string `json:"label"`
			Up    int64  `json:"up"`
		} `json:"items"`
		Metadata struct {
			Label string `json:"label"`
		} `json:"metadata"`
	}
	if err := decodeStrictJSONObject([]byte(raw), &dst); err != nil {
		t.Fatalf("decode valid nested document: %v", err)
	}
	if dst.ReporterID != "same" || len(dst.Items) != 2 || dst.Metadata.Label != "same" {
		t.Fatalf("decoded value=%+v", dst)
	}
}

func TestDecodeStrictJSONObjectAcceptsTrailingWhitespaceOnly(t *testing.T) {
	var dst struct {
		Value string `json:"value"`
	}
	if err := decodeStrictJSONObject([]byte("{\"value\":\"ok\"}\n\t "), &dst); err != nil {
		t.Fatalf("decode document with whitespace: %v", err)
	}
	if strings.TrimSpace(dst.Value) != "ok" {
		t.Fatalf("value=%q", dst.Value)
	}
}

func TestRejectDuplicateJSONKeysEnforcesDepthLimit(t *testing.T) {
	atLimit := strings.Repeat(`{"nested":`, maxStrictJSONDepth) + `0` + strings.Repeat(`}`, maxStrictJSONDepth)
	if err := rejectDuplicateJSONKeys([]byte(atLimit)); err != nil {
		t.Fatalf("document at depth limit rejected: %v", err)
	}

	aboveLimit := strings.Repeat(`{"nested":`, maxStrictJSONDepth+1) + `0` + strings.Repeat(`}`, maxStrictJSONDepth+1)
	if err := rejectDuplicateJSONKeys([]byte(aboveLimit)); err == nil {
		t.Fatal("document above depth limit succeeded")
	}
}
