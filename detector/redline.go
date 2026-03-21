package detector

import (
	"fmt"
	"io"
	"time"

	"archive/zip"

	"github.com/golang/snappy"
	"github.com/user/iwork-redline-detector/iwa"
)

type Change struct {
	Kind    int
	Author  string
	Date    time.Time
	Hidden  bool
	Content string
}

type RedlineDetection struct {
	TrackChangesStatus TrackChangesStatus
	SettingEnabled     bool
	SettingPaused      bool
	HighConfidence     bool

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

	docData, err := extractDocumentIWA(pagesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract Document.iwa: %w", err)
	}

	if len(docData) < 4 {
		return nil, fmt.Errorf("Document.iwa too short: %d bytes", len(docData))
	}

	iwaFile, err := iwa.ParseIWAFile(docData)
	protobufParsed := (err == nil)

	if protobufParsed {
		if docMsgs, ok := iwaFile.Messages[TypeDocumentArchive]; ok && len(docMsgs) > 0 {
			result.HighConfidence = true
			parseDocumentArchive(docMsgs[0], result)
		}
	}

	decompressed, err := snappy.Decode(nil, docData[4:])
	if err != nil {
		if !protobufParsed {
			return nil, fmt.Errorf("failed to decompress Document.iwa: %w", err)
		}
		insertions, deletions := 0, 0
		result.InsertionCount = insertions
		result.DeletionCount = deletions

		if result.HighConfidence {
			if result.SettingEnabled {
				result.TrackChangesStatus = TCStatusEnabledNoChanges
			} else {
				result.TrackChangesStatus = TCStatusDisabled
			}
		}
	} else {
		hasTrackChanges, insertions, deletions := detectTrackChangesHeuristic(decompressed)
		result.InsertionCount = insertions
		result.DeletionCount = deletions

		if result.HighConfidence {
			if result.SettingEnabled && (insertions > 21 || deletions > 1) {
				result.TrackChangesStatus = TCStatusEnabledWithChanges
			} else if result.SettingEnabled {
				result.TrackChangesStatus = TCStatusEnabledNoChanges
			} else {
				result.TrackChangesStatus = TCStatusDisabled
			}
		} else {
			if hasTrackChanges {
				result.TrackChangesStatus = TCStatusEnabledWithChanges
				result.SettingEnabled = true
			}
		}
	}

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
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "Index/AnnotationAuthorStorage.iwa" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("AnnotationAuthorStorage.iwa not found")
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

func (r *RedlineDetection) HasTrackedChanges() bool {
	return r.TrackChangesStatus == TCStatusEnabledWithChanges
}
