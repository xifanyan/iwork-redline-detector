package iwa

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMessage_PreservesRepeatedAndNestedFields(t *testing.T) {
	child := []byte{0x08, 0x2a}
	data := []byte{
		0x08, 0x01,
		0x08, 0x02,
		0x12, byte(len(child)),
	}
	data = append(data, child...)

	msg, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage returned error: %v", err)
	}

	field1 := msg.FieldsByNumber(1)
	if len(field1) != 2 {
		t.Fatalf("FieldsByNumber(1) len = %d, want 2", len(field1))
	}

	if val, ok := field1[0].AsVarint(); !ok || val != 1 {
		t.Fatalf("first repeated field value = (%d, %v), want (1, true)", val, ok)
	}
	if val, ok := field1[1].AsVarint(); !ok || val != 2 {
		t.Fatalf("second repeated field value = (%d, %v), want (2, true)", val, ok)
	}

	nested := msg.NestedMessages(2)
	if len(nested) != 1 {
		t.Fatalf("NestedMessages(2) len = %d, want 1", len(nested))
	}

	if val, ok := nested[0].FirstVarint(1); !ok || val != 42 {
		t.Fatalf("nested FirstVarint(1) = (%d, %v), want (42, true)", val, ok)
	}
}

func TestParseMessage_NonMessageBytesDoNotParseAsNestedMessage(t *testing.T) {
	msg, err := ParseMessage([]byte{0x12, 0x03, 0xff, 0x00, 0x80})
	if err != nil {
		t.Fatalf("ParseMessage returned error: %v", err)
	}

	field, ok := msg.FirstField(2)
	if !ok {
		t.Fatal("FirstField(2) = false, want true")
	}

	if _, err := field.AsMessage(); err == nil {
		t.Fatal("AsMessage expected error for opaque bytes")
	}
}

func TestParseMessageData_PreservesFieldsWhenTypedParserFails(t *testing.T) {
	data := []byte{
		0x08, 0x01,
		0x1b,
		0x08, 0x02,
		0x1c,
		0x12, 0x01, 0x03,
	}

	parsed := ParseMessageData(data)

	if got := parsed.Fields[1]; len(got) != 1 || got[0] != 0x01 {
		t.Fatalf("Fields[1] = %v, want [1]", got)
	}
	if got := parsed.Fields[2]; len(got) != 1 || got[0] != 0x03 {
		t.Fatalf("Fields[2] = %v, want [3]", got)
	}
}

func TestExtractDocumentIWA(t *testing.T) {
	testdataDir := filepath.Join("..", "testdata", "pages")

	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{name: "normal pages", filename: "normal.pages", wantErr: false},
		{name: "tracking pages", filename: "track.not-accepted.pages", wantErr: false},
		{name: "blank tracking pages", filename: "blank.track.pages", wantErr: false},
		{name: "deletion pages", filename: "deletion.track-paused.pages", wantErr: false},
		{name: "format 2013 (index.zip at root)", filename: "../pages2013/normal.2013.pages", wantErr: false},
		{name: "nonexistent file", filename: "nonexistent.pages", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pagesPath := filepath.Join(testdataDir, tt.filename)

			data, err := ExtractDocumentIWA(pagesPath)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ExtractDocumentIWA(%s) expected error, got nil", tt.name)
				}
				return
			}

			if err != nil {
				t.Errorf("ExtractDocumentIWA(%s) returned error: %v", tt.name, err)
				return
			}

			if len(data) == 0 {
				t.Errorf("ExtractDocumentIWA(%s) returned empty data", tt.name)
			}
		})
	}
}

func TestExtractDocumentIWA_LegacyUnsupported(t *testing.T) {
	pagesPath := filepath.Join("..", "testdata", "pages09", "normal.pages")
	_, err := ExtractDocumentIWA(pagesPath)
	if err == nil {
		t.Fatal("ExtractDocumentIWA expected error for legacy Pages '09 bundle")
	}
}

func TestExtractAnnotationStorageIWA(t *testing.T) {
	testdataDir := filepath.Join("..", "testdata", "pages")

	_, err := ExtractAnnotationStorageIWA(filepath.Join(testdataDir, "normal.pages"))
	if err != nil {
		t.Logf("ExtractAnnotationStorageIWA returned error (may be expected): %v", err)
	}

	_, err = ExtractAnnotationStorageIWA("/nonexistent/pages.pages")
	if err == nil {
		t.Error("ExtractAnnotationStorageIWA expected error for nonexistent file")
	}
}

func TestDecompressSnappy(t *testing.T) {
	testdataDir := filepath.Join("..", "testdata", "pages")

	tests := []struct {
		name       string
		filename   string
		wantLen    int
		wantErr    bool
		emptyInput bool
	}{
		{name: "normal pages", filename: "normal.pages", wantErr: false},
		{name: "tracking pages", filename: "track.not-accepted.pages", wantErr: false},
		{name: "empty data", emptyInput: true, wantLen: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data []byte
			var err error

			if !tt.emptyInput {
				pagesPath := filepath.Join(testdataDir, tt.filename)
				data, err = ExtractDocumentIWA(pagesPath)
				if err != nil {
					t.Skipf("skipping %s: failed to extract IWA: %v", tt.name, err)
				}
			} else {
				data = []byte{}
			}

			decompressed, err := DecompressSnappy(data)
			if tt.wantErr {
				if err == nil {
					t.Errorf("DecompressSnappy expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("DecompressSnappy returned error: %v", err)
				return
			}

			if tt.wantLen > 0 && len(decompressed) != tt.wantLen {
				t.Errorf("DecompressSnappy decompressed to %d bytes, want %d", len(decompressed), tt.wantLen)
			}

			if len(decompressed) == 0 {
				t.Errorf("DecompressSnappy returned empty result")
			}
		})
	}
}

func TestDecompressSnappy_ConcatenatedRawSegments(t *testing.T) {
	pagesPath := filepath.Join("..", "..", "pages", "0010h000000006516712.pages")
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("skipping concatenated raw snappy test: sample not found: %v", err)
		}
		t.Fatalf("failed to open sample pages file: %v", err)
	}
	defer r.Close()

	var data []byte
	for _, f := range r.File {
		if f.Name != "Index/Document.iwa" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("failed to open Document.iwa: %v", err)
		}
		data, err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("failed to read Document.iwa: %v", err)
		}
		break
	}
	if len(data) == 0 {
		t.Fatal("Document.iwa not found in sample pages file")
	}

	decompressed, err := DecompressSnappy(data)
	if err != nil {
		t.Fatalf("DecompressSnappy returned error for concatenated segments: %v", err)
	}
	if len(decompressed) <= 65536 {
		t.Fatalf("decompressed length = %d, want more than first 65536-byte segment", len(decompressed))
	}
}

func TestParseIWAFile_ParsesStreamOfArchiveInfoAndPayloads(t *testing.T) {
	pagesPath := filepath.Join("..", "testdata", "pages", "track.not-accepted.pages")
	data, err := ExtractDocumentIWA(pagesPath)
	if err != nil {
		t.Fatalf("ExtractDocumentIWA returned error: %v", err)
	}

	parsed, err := ParseIWAFile(data)
	if err != nil {
		t.Fatalf("ParseIWAFile returned error: %v", err)
	}
	if len(parsed.Records) == 0 {
		t.Fatal("ParseIWAFile returned no records")
	}
	if len(parsed.ByType[10000]) == 0 {
		t.Fatal("ParseIWAFile missing expected TP.DocumentArchive records")
	}
	if len(parsed.ByType[2060]) == 0 {
		t.Fatal("ParseIWAFile missing expected visible change records for tracked sample")
	}
	if len(parsed.ByType[0]) != 0 {
		t.Fatalf("ParseIWAFile produced %d type=0 records, want 0", len(parsed.ByType[0]))
	}
	if parsed.ArchiveInfo == nil {
		t.Fatal("ParseIWAFile ArchiveInfo = nil")
	}
}

func TestParserCountPattern(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		pattern  []byte
		expected int
	}{
		{name: "basic pattern", data: []byte{0x08, 0x01, 0x12, 0x00, 0x08, 0x01, 0x12}, pattern: []byte{0x08, 0x01, 0x12}, expected: 2},
		{name: "no match", data: []byte{0x00, 0x01, 0x02}, pattern: []byte{0x08}, expected: 0},
		{name: "empty data", data: []byte{}, pattern: []byte{0x08}, expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countPattern(tt.data, tt.pattern)
			if result != tt.expected {
				t.Errorf("countPattern() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func countPattern(data, pattern []byte) int {
	count := 0
	for i := 0; i <= len(data)-len(pattern); i++ {
		found := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				found = false
				break
			}
		}
		if found {
			count++
			i += len(pattern) - 1
		}
	}
	return count
}

func TestHasNonEmptyField(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		fieldNum uint64
		expected bool
	}{
		{name: "field exists with data", data: []byte{0x08, 0x01, 0x12, 0x04, 0x00, 0x00, 0x00, 0x00}, fieldNum: 1, expected: true},
		{name: "field does not exist", data: []byte{0x08, 0x01}, fieldNum: 2, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasNonEmptyField(tt.data, tt.fieldNum)
			if result != tt.expected {
				t.Errorf("HasNonEmptyField() = %v, want %v", result, tt.expected)
			}
		})
	}
}
