package detector

import "fmt"

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
