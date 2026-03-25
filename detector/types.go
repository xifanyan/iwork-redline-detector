package detector

import (
	"archive/zip"
	"fmt"
)

type TrackChangesStatus int
type FormatType int

const (
	TCStatusUnknown TrackChangesStatus = iota
	TCStatusDisabled
	TCStatusPaused
	TCStatusEnabledNoChanges
	TCStatusEnabledWithChanges
)

const (
	FormatUnknown FormatType = iota
	FormatModernIWA
	FormatLegacyXML
	FormatEncrypted
)

const (
	TypeChangeCTVisibilityCommand  uint64 = 10148
	TypeTrackChangesCommand        uint64 = 10149
	TypePauseChangeTrackingCommand uint64 = 10157
	TypeHighlight                  uint64 = 2013
	TypeCommentInfo                uint64 = 2014
	TypeCommentStorage             uint64 = 3056
	TypeCommentStorageApply        uint64 = 3060
	TypeDrawableInfoComment        uint64 = 3057
	TypeAnnotationAuthor           uint64 = 211
	TypeAnnotationAuthorStorage    uint64 = 212
)

const (
	TypeTextStorageArchive   uint64 = 1001
	TypeDocumentArchive      uint64 = 1002
	TypeSettingsArchive      uint64 = 1003
	TypeChangeArchive        uint64 = 1004
	TypeChangeSessionArchive uint64 = 1005
)

const (
	FieldChangeTrackingEnabled = 40
	FieldChangeKind            = 1
	FieldChangeSession         = 2
	FieldChangeDate            = 3
	FieldChangeHidden          = 4
)

const (
	ChangeKindInsertion = 1
	ChangeKindDeletion  = 2
)

var TypeNames = map[uint64]string{
	TypeChangeCTVisibilityCommand:  "TP.ChangeCTVisibilityCommandArchive",
	TypeTrackChangesCommand:        "TP.TrackChangesCommandArchive",
	TypePauseChangeTrackingCommand: "TP.PauseChangeTrackingCommandArchive",
	TypeHighlight:                  "TSWP.HighlightArchive",
	TypeCommentInfo:                "TSWP.CommentInfoArchive",
	TypeCommentStorage:             "TSD.CommentStorageArchive",
	TypeCommentStorageApply:        "TSD.CommentStorageApplyCommandArchive",
	TypeDrawableInfoComment:        "TSD.DrawableInfoCommentCommandArchive",
	TypeAnnotationAuthor:           "TSK.AnnotationAuthorArchive",
	TypeAnnotationAuthorStorage:    "TSK.AnnotationAuthorStorageArchive",
	TypeTextStorageArchive:         "TSWP.TextStorageArchive",
	TypeDocumentArchive:            "TP.DocumentArchive",
	TypeSettingsArchive:            "TP.SettingsArchive",
	TypeChangeArchive:              "TSWP.ChangeArchive",
	TypeChangeSessionArchive:       "TSWP.ChangeSessionArchive",
}

func GetTypeName(typeID uint64) string {
	if name, ok := TypeNames[typeID]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", typeID)
}

func (s TrackChangesStatus) String() string {
	switch s {
	case TCStatusDisabled:
		return "Disabled"
	case TCStatusPaused:
		return "Paused"
	case TCStatusEnabledNoChanges:
		return "Enabled (No Changes)"
	case TCStatusEnabledWithChanges:
		return "Enabled (With Changes)"
	default:
		return "Unknown"
	}
}

func (f FormatType) String() string {
	switch f {
	case FormatModernIWA:
		return "Modern"
	case FormatLegacyXML:
		return "Pages '09"
	case FormatEncrypted:
		return "Encrypted"
	default:
		return "Unknown"
	}
}

func DetectFormat(pagesPath string) FormatType {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return FormatUnknown
	}
	defer r.Close()

	hasModern := false
	hasLegacy := false
	for _, entry := range r.File {
		if entry.Name == "Index/Document.iwa" {
			hasModern = true
		}
		if entry.Name == "index.xml" || entry.Name == "index.xml.gz" {
			hasLegacy = true
		}
	}

	if hasModern {
		return FormatModernIWA
	}
	if hasLegacy {
		return FormatLegacyXML
	}

	return FormatUnknown
}
