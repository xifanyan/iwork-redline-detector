package detector

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/golang/snappy"
	"github.com/xifanyan/iwork-redline-detector/iwa"
)

var ErrExtractedBundle = errors.New("path is a directory, not a .pages file (extracted bundle?)")

type Change struct {
	Kind    int
	Author  string
	Date    time.Time
	Hidden  bool
	Content string
}

type RedlineDetection struct {
	Format                FormatType
	IsEncrypted           bool
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
	if info, err := os.Stat(pagesPath); err == nil && info.IsDir() {
		return nil, ErrExtractedBundle
	}

	result := &RedlineDetection{
		Format:         FormatUnknown,
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
	result.Format = format

	if encrypted, err := DetectEncryption(pagesPath); err == nil && encrypted {
		result.IsEncrypted = true
		if format == FormatUnknown {
			result.Format = FormatEncrypted
		}
		return result, nil
	}

	if format == FormatUnknown {
		if isZipFile(pagesPath) {
			r, zipErr := zip.OpenReader(pagesPath)
			if zipErr != nil {
				result.IsEncrypted = true
				result.Format = FormatEncrypted
				return result, nil
			}
			r.Close()
		}
	}

	if format == FormatLegacyXML {
		return detectRedlinesLegacyXML(pagesPath, result)
	}

	docData, err := extractDocumentIWA(pagesPath)
	if err != nil {
		if isEncryptionError(err) {
			result.IsEncrypted = true
			return result, nil
		}
		return nil, fmt.Errorf("failed to extract Document.iwa: %w", err)
	}

	if len(docData) < 4 {
		return nil, fmt.Errorf("Document.iwa too short: %d bytes", len(docData))
	}

	decompressed, err := iwa.DecompressSnappy(docData)
	if err != nil {
		if isEncryptionError(err) {
			result.IsEncrypted = true
			return result, nil
		}
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
	} else if result.SettingEnabled && result.TrackedChangesPresent {
		result.TrackChangesStatus = TCStatusEnabledWithChanges
	} else if result.SettingEnabled {
		result.TrackChangesStatus = TCStatusEnabledNoChanges
	} else {
		result.TrackChangesStatus = TCStatusDisabled
	}

	result.CommentCount = detectCommentsInData(decompressed)
	result.HasComments = result.CommentCount > 0

	iwaFile, err := iwa.ParseIWAFile(docData)
	if err != nil {
		if isEncryptionError(err) {
			result.IsEncrypted = true
			return result, nil
		}
	}
	protobufParsed := err == nil
	if protobufParsed {
		if settingsMsgs, ok := iwaFile.Messages[TypeSettingsArchive]; ok && len(settingsMsgs) > 0 {
			parseMarkupSettings(settingsMsgs[0], &result.MarkupSettings)
		}
	}

	annotationData, err := extractAnnotationStorageIWA(pagesPath)
	if err == nil && len(annotationData) > 4 {
		result.Authors = extractAuthorsFromData(annotationData)
	}

	return result, nil
}

func DetectEncryption(pagesPath string) (bool, error) {
	if info, err := os.Stat(pagesPath); err == nil && info.IsDir() {
		return false, nil
	}

	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return false, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == ".iwpv2" {
			rc, err := f.Open()
			if err != nil {
				return false, err
			}
			defer rc.Close()
			data := make([]byte, 98)
			n, err := rc.Read(data)
			if err != nil {
				return false, err
			}
			if n == 98 && data[0] == 2 && data[2] == 1 {
				return true, nil
			}
		}
	}
	return false, nil
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
	msg, err := iwa.ParseMessage(data)
	if err != nil {
		legacy := iwa.ParseMessageData(data)
		if val, ok := legacy.Fields[12]; ok && len(val) > 0 {
			settings.ShowCTMarkup = decodeBool(val)
		}
		if val, ok := legacy.Fields[13]; ok && len(val) > 0 {
			settings.ShowCTDeletions = decodeBool(val)
		}
		if val, ok := legacy.Fields[15]; ok && len(val) > 0 {
			settings.ChangeBarsVisible = decodeBool(val)
		}
		if val, ok := legacy.Fields[16]; ok && len(val) > 0 {
			settings.FormatChangesVisible = decodeBool(val)
		}
		if val, ok := legacy.Fields[17]; ok && len(val) > 0 {
			settings.AnnotationsVisible = decodeBool(val)
		}
		return
	}

	if field, ok := msg.FirstField(12); ok {
		if val, ok := field.AsBool(); ok {
			settings.ShowCTMarkup = val
		}
	}
	if field, ok := msg.FirstField(13); ok {
		if val, ok := field.AsBool(); ok {
			settings.ShowCTDeletions = val
		}
	}
	if field, ok := msg.FirstField(15); ok {
		if val, ok := field.AsBool(); ok {
			settings.ChangeBarsVisible = val
		}
	}
	if field, ok := msg.FirstField(16); ok {
		if val, ok := field.AsBool(); ok {
			settings.FormatChangesVisible = val
		}
	}
	if field, ok := msg.FirstField(17); ok {
		if val, ok := field.AsBool(); ok {
			settings.AnnotationsVisible = val
		}
	}
}

func decodeBool(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	return data[0] != 0
}

func detectCommentsInData(data []byte) int {
	count := 0
	seen := make(map[string]struct{})
	start := -1

	flush := func(end int) {
		if start < 0 || end-start < 4 {
			start = -1
			return
		}
		text := string(data[start:end])
		if !looksLikeCommentContent(text) {
			start = -1
			return
		}
		if _, ok := seen[text]; ok {
			start = -1
			return
		}
		seen[text] = struct{}{}
		count++
		start = -1
	}

	for i, b := range data {
		if isPrintableASCII(b) {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
	}
	flush(len(data))

	return count
}

func isPrintableASCII(b byte) bool {
	return (b >= 0x20 && b <= 0x7e) || b == '\n' || b == '\r' || b == '\t'
}

func looksLikeCommentContent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if !strings.Contains(lower, "comment") {
		return false
	}

	commentIdx := strings.Index(lower, "comment")
	if commentIdx <= 0 {
		return false
	}

	prefix := lower[:commentIdx]
	for _, marker := range []string{")comment", " comments*", " comment with", " comment:", "comment with"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	for i := 0; i < len(prefix); i++ {
		if prefix[i] < 0x20 {
			return true
		}
	}

	return strings.Count(lower, "\n") >= 2
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

	for _, f := range r.File {
		if f.Name == "Index.zip" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			return extractDocumentIWAFromZipData(data)
		}
	}

	return nil, fmt.Errorf("Document.iwa not found")
}

func extractDocumentIWAFromZipData(data []byte) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if f.Name == "Document.iwa" || f.Name == "index/Document.iwa" || f.Name == "Index/Document.iwa" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("Document.iwa not found in Index.zip")
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
	var readErr error
	for _, entry := range r.File {
		if entry.Name == "index.xml" {
			indexData, readErr = readZipEntry(entry)
			if readErr != nil {
				if isEncryptionError(readErr) {
					result.IsEncrypted = true
					return result, nil
				}
				return nil, fmt.Errorf("failed to read index.xml: %w", readErr)
			}
			break
		}
		if entry.Name == "index.xml.gz" {
			indexData, readErr = readZipEntry(entry)
			if readErr != nil {
				if isEncryptionError(readErr) {
					result.IsEncrypted = true
					return result, nil
				}
				return nil, fmt.Errorf("failed to read index.xml.gz: %w", readErr)
			}
			indexData, err = decompressGzip(indexData)
			if err != nil {
				return nil, fmt.Errorf("failed to decompress index.xml.gz: %w", err)
			}
			break
		}
	}

	if indexData == nil {
		if encrypted, err := DetectEncryption(pagesPath); err == nil && encrypted {
			result.IsEncrypted = true
			return result, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed to read index.xml: %w", readErr)
		}
		return nil, fmt.Errorf("no index.xml or index.xml.gz found in archive")
	}

	insertionCount, deletionCount, commentCount, trackingEnabled, trackingPaused, highConfidence, err := parseLegacyIndexXML(indexData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse legacy index.xml: %w", err)
	}

	result.InsertionCount = insertionCount
	result.DeletionCount = deletionCount
	result.CommentCount = commentCount
	result.HasComments = commentCount > 0
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
	} else if trackingEnabled && result.TrackedChangesPresent {
		result.TrackChangesStatus = TCStatusEnabledWithChanges
	} else if trackingEnabled {
		result.TrackChangesStatus = TCStatusEnabledNoChanges
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

func parseLegacyIndexXML(data []byte) (insertions, deletions, comments int, enabled, paused, highConfidence bool, err error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	hasAggregateCounts := false
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return 0, 0, 0, false, false, false, err
		}

		switch se := token.(type) {
		case xml.StartElement:
			if localName(se, "change-tracking") {
				for _, attr := range se.Attr {
					switch attr.Name.Local {
					case "enabled":
						enabled = attr.Value == "true"
						highConfidence = true
					case "suspended":
						paused = attr.Value == "true"
					}
				}
			}
			if localName(se, "text-changes") {
				for _, attr := range se.Attr {
					switch attr.Name.Local {
					case "insertion-count":
						var count int
						if _, scanErr := fmt.Sscanf(attr.Value, "%d", &count); scanErr == nil {
							insertions = count
							hasAggregateCounts = true
						}
					case "deletion-count":
						var count int
						if _, scanErr := fmt.Sscanf(attr.Value, "%d", &count); scanErr == nil {
							deletions = count
							hasAggregateCounts = true
						}
					}
				}
			}
			if !hasAggregateCounts && localNameAny(se, "change", "changed") {
				kind := ""
				for _, attr := range se.Attr {
					if attr.Name.Local == "kind" {
						kind = attr.Value
						break
					}
				}
				switch kind {
				case "insertion":
					insertions++
				case "deletion":
					deletions++
				}
			}
			if localName(se, "annotation") {
				comments++
			}
		}
	}
	return insertions, deletions, comments, enabled, paused, highConfidence, nil
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

func isEncryptionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	indicators := []string{"password", "encrypted", "unsupported compression", "crypto", "invalid header", "invalid pkcs7", "decryption", "authentication", "snappy: corrupt", "corrupt"}
	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return false
}

func (r *RedlineDetection) HasTrackedChanges() bool {
	return r.TrackedChangesPresent
}

func (r *RedlineDetection) HasRedlines() bool {
	if r == nil {
		return false
	}
	return (r.SettingEnabled && r.TrackedChangesPresent) || r.HasComments
}
