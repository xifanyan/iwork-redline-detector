package detector

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/golang/snappy"
	"github.com/xifanyan/iwork-redline-detector/iwa"
)

type Change struct {
	Kind    int
	Author  string
	Date    time.Time
	Hidden  bool
	Content string
}

type RedlineDetection struct {
	TrackChangesStatus    TrackChangesStatus
	SettingEnabled        bool
	SettingPaused         bool
	TrackedChangesPresent bool
	HighConfidence        bool

	InsertionCount int
	DeletionCount  int
	HiddenChanges  int

	Changes []Change

	HasComments  bool
	CommentCount int

	MarkupSettings MarkupSettings

	Authors []Author
}

type MarkupSettings struct {
	ShowCTMarkup         bool
	ShowCTDeletions      bool
	ChangeBarsVisible    bool
	FormatChangesVisible bool
	AnnotationsVisible   bool
}

type Author struct {
	Name  string
	Color string
}

func DetectRedlines(pagesPath string) (*RedlineDetection, error) {
	result := &RedlineDetection{
		HighConfidence: false,
		MarkupSettings: MarkupSettings{
			ShowCTMarkup:         true,
			ShowCTDeletions:      true,
			ChangeBarsVisible:    true,
			FormatChangesVisible: true,
			AnnotationsVisible:   true,
		},
	}

	format := DetectFormat(pagesPath)

	if format == FormatLegacyXML {
		return detectRedlinesLegacyXML(pagesPath, result)
	}

	docData, err := extractDocumentIWA(pagesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract Document.iwa: %w", err)
	}

	if len(docData) < 4 {
		return nil, fmt.Errorf("Document.iwa too short: %d bytes", len(docData))
	}

	decompressed, err := iwa.DecompressSnappy(docData)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress Document.iwa: %w", err)
	}

	hasTrackChanges, insertions, deletions := detectTrackChangesHeuristic(decompressed)
	result.InsertionCount = insertions
	result.DeletionCount = deletions
	result.TrackedChangesPresent = hasTrackChanges

	if enabled, ok := detectBooleanFieldValue(decompressed, FieldChangeTrackingEnabled); ok {
		result.SettingEnabled = enabled
		result.HighConfidence = true
	}

	if viewStateData, err := extractViewStateIWA(pagesPath); err == nil {
		if viewStateDecompressed, err := iwa.DecompressSnappy(viewStateData); err == nil {
			if paused, ok := detectBooleanFieldValue(viewStateDecompressed, 28); ok {
				result.SettingPaused = paused
				result.HighConfidence = true
			}
		}
	}

	if result.HighConfidence {
		switch {
		case result.SettingPaused:
			result.TrackChangesStatus = TCStatusPaused
		case result.SettingEnabled && result.TrackedChangesPresent:
			result.TrackChangesStatus = TCStatusEnabledWithChanges
		case result.SettingEnabled:
			result.TrackChangesStatus = TCStatusEnabledNoChanges
		default:
			result.TrackChangesStatus = TCStatusDisabled
		}
	} else if result.TrackedChangesPresent {
		result.TrackChangesStatus = TCStatusEnabledWithChanges
	} else {
		result.TrackChangesStatus = TCStatusDisabled
	}

	iwaFile, err := iwa.ParseIWAFile(docData)
	protobufParsed := err == nil
	if protobufParsed {
		if settingsMsgs, ok := iwaFile.Messages[TypeSettingsArchive]; ok && len(settingsMsgs) > 0 {
			parseMarkupSettings(settingsMsgs[0], &result.MarkupSettings)
		}
		result.HasComments = detectComments(iwaFile)
	}

	annotationData, err := extractAnnotationStorageIWA(pagesPath)
	if err == nil && len(annotationData) > 4 {
		result.Authors = extractAuthorsFromData(annotationData)
	}

	return result, nil
}

func parseDocumentArchive(data []byte, result *RedlineDetection) {
	msg := iwa.ParseMessageData(data)

	if val, ok := msg.Fields[FieldChangeTrackingEnabled]; ok && len(val) > 0 {
		result.SettingEnabled = decodeBool(val)
	}
	if val, ok := msg.Fields[FieldChangeTrackingPaused]; ok && len(val) > 0 {
		result.SettingPaused = decodeBool(val)
	}
}

func detectBooleanFieldValue(data []byte, fieldNum uint64) (bool, bool) {
	tag := encodeVarint(fieldNum << 3)
	for offset := 0; offset < len(data); {
		idx := bytes.Index(data[offset:], tag)
		if idx < 0 {
			break
		}
		idx += offset
		if idx+len(tag) >= len(data) {
			return false, false
		}
		value := data[idx+len(tag)]
		if value == 0 || value == 1 {
			return value == 1, true
		}
		offset = idx + 1
	}
	return false, false
}

func encodeVarint(val uint64) []byte {
	buf := make([]byte, 0, 10)
	for val >= 0x80 {
		buf = append(buf, byte(val)|0x80)
		val >>= 7
	}
	return append(buf, byte(val))
}

func parseMarkupSettings(data []byte, settings *MarkupSettings) {
	msg := iwa.ParseMessageData(data)

	if val, ok := msg.Fields[12]; ok && len(val) > 0 {
		settings.ShowCTMarkup = decodeBool(val)
	}
	if val, ok := msg.Fields[13]; ok && len(val) > 0 {
		settings.ShowCTDeletions = decodeBool(val)
	}
	if val, ok := msg.Fields[15]; ok && len(val) > 0 {
		settings.ChangeBarsVisible = decodeBool(val)
	}
	if val, ok := msg.Fields[16]; ok && len(val) > 0 {
		settings.FormatChangesVisible = decodeBool(val)
	}
	if val, ok := msg.Fields[17]; ok && len(val) > 0 {
		settings.AnnotationsVisible = decodeBool(val)
	}
}

func decodeBool(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	return data[0] != 0
}

func detectComments(iwaFile *iwa.IWAFile) bool {
	if highlightMsgs, ok := iwaFile.Messages[TypeHighlight]; ok && len(highlightMsgs) > 0 {
		for _, msg := range highlightMsgs {
			parsed := iwa.ParseMessageData(msg)
			if _, ok := parsed.Fields[1]; ok {
				return true
			}
		}
	}
	if commentMsgs, ok := iwaFile.Messages[TypeCommentInfo]; ok && len(commentMsgs) > 0 {
		for _, msg := range commentMsgs {
			parsed := iwa.ParseMessageData(msg)
			if _, ok := parsed.Fields[2]; ok {
				return true
			}
		}
	}
	return false
}

func extractDocumentIWA(pagesPath string) ([]byte, error) {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "Index/Document.iwa" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("Document.iwa not found")
}

func extractAnnotationStorageIWA(pagesPath string) ([]byte, error) {
	return extractIndexIWAByPrefix(pagesPath, "AnnotationAuthorStorage")
}

func extractViewStateIWA(pagesPath string) ([]byte, error) {
	return extractIndexIWAByPrefix(pagesPath, "ViewState")
}

func extractIndexIWAByPrefix(pagesPath string, prefix string) ([]byte, error) {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		name := strings.TrimPrefix(f.Name, "Index/")
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".iwa") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("%s.iwa not found", prefix)
}

func detectTrackChangesHeuristic(data []byte) (hasTrackChanges bool, insertions, deletions int) {
	kind1Count := countPattern(data, []byte{0x08, 0x01, 0x12})
	kind2Count := countPattern(data, []byte{0x08, 0x02, 0x12})

	insertions = kind1Count
	deletions = kind2Count

	if kind1Count > 21 || kind2Count > 1 {
		hasTrackChanges = true
	}

	return hasTrackChanges, insertions, deletions
}

func extractAuthorsFromData(data []byte) []Author {
	if len(data) < 4 {
		return nil
	}

	decoded, err := snappy.Decode(nil, data[4:])
	if err != nil {
		return nil
	}

	var authors []Author

	authorNames := []string{"Paul", "John", "Jane", "Admin", "User"}
	for _, name := range authorNames {
		if contains(decoded, []byte(name)) {
			authors = append(authors, Author{Name: name})
		}
	}

	return authors
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

func contains(data, pattern []byte) bool {
	for i := 0; i <= len(data)-len(pattern); i++ {
		found := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}

func detectRedlinesLegacyXML(pagesPath string, result *RedlineDetection) (*RedlineDetection, error) {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open pages file: %w", err)
	}
	defer r.Close()

	var indexData []byte
	for _, entry := range r.File {
		if entry.Name == "index.xml" {
			indexData, err = readZipEntry(entry)
			if err != nil {
				return nil, fmt.Errorf("failed to read index.xml: %w", err)
			}
			break
		}
		if entry.Name == "index.xml.gz" {
			indexData, err = readZipEntry(entry)
			if err != nil {
				return nil, fmt.Errorf("failed to read index.xml.gz: %w", err)
			}
			indexData, err = decompressGzip(indexData)
			if err != nil {
				return nil, fmt.Errorf("failed to decompress index.xml.gz: %w", err)
			}
			break
		}
	}

	if indexData == nil {
		return nil, fmt.Errorf("no index.xml or index.xml.gz found in archive")
	}

	insertionCount, deletionCount, trackingEnabled, trackingPaused, highConfidence := parseLegacyIndexXML(indexData)

	result.InsertionCount = insertionCount
	result.DeletionCount = deletionCount
	result.SettingEnabled = trackingEnabled
	result.SettingPaused = trackingPaused
	result.HighConfidence = highConfidence
	result.TrackedChangesPresent = insertionCount > 0 || deletionCount > 0

	if highConfidence {
		switch {
		case trackingPaused:
			result.TrackChangesStatus = TCStatusPaused
		case trackingEnabled && result.TrackedChangesPresent:
			result.TrackChangesStatus = TCStatusEnabledWithChanges
		case trackingEnabled:
			result.TrackChangesStatus = TCStatusEnabledNoChanges
		default:
			result.TrackChangesStatus = TCStatusDisabled
		}
	} else if result.TrackedChangesPresent {
		result.TrackChangesStatus = TCStatusEnabledWithChanges
	} else {
		result.TrackChangesStatus = TCStatusDisabled
	}

	return result, nil
}

func readZipEntry(entry *zip.File) ([]byte, error) {
	rc, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func decompressGzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func parseLegacyIndexXML(data []byte) (insertions, deletions int, enabled, paused, highConfidence bool) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch se := token.(type) {
		case xml.StartElement:
			if localName(se, "change-tracking") {
				for _, attr := range se.Attr {
					if attr.Name.Local == "enabled" {
						enabled = attr.Value == "true"
						highConfidence = true
					}
					if attr.Name.Local == "suspended" {
						paused = attr.Value == "true"
					}
				}
			}
			if localName(se, "text-changes") {
				for _, attr := range se.Attr {
					if attr.Name.Local == "sf:insertion-count" || attr.Name.Local == "insertion-count" {
						var count int
						fmt.Sscanf(attr.Value, "%d", &count)
						insertions = count
					}
					if attr.Name.Local == "sf:deletion-count" || attr.Name.Local == "deletion-count" {
						var count int
						fmt.Sscanf(attr.Value, "%d", &count)
						deletions = count
					}
				}
			}
			if localNameAny(se, "change", "changed", "sf:change", "sf:changed") {
				kind := ""
				for _, attr := range se.Attr {
					if attr.Name.Local == "kind" || attr.Name.Local == "sf:kind" {
						kind = attr.Value
					}
				}
				if kind == "insertion" {
					insertions++
				} else if kind == "deletion" {
					deletions++
				}
			}
		}
	}
	return
}

func localName(se xml.StartElement, name string) bool {
	return se.Name.Local == name
}

func localNameAny(se xml.StartElement, names ...string) bool {
	for _, name := range names {
		if se.Name.Local == name {
			return true
		}
	}
	return false
}

func (r *RedlineDetection) HasTrackedChanges() bool {
	return r.TrackedChangesPresent
}
